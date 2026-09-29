package backup

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// libexecGlob is where Alpine installs each PostgreSQL major's client
// programs; the API image carries several (see Dockerfile).
var libexecGlob = "/usr/libexec/postgresql*/"

// pgTool finds a client program for the server's major version: the same
// major, else the oldest newer one (they read older servers), else PATH.
func pgTool(name string, serverMajor int) string {
	matches, _ := filepath.Glob(libexecGlob + name)
	best, bestMajor := "", 0
	for _, match := range matches {
		major, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(filepath.Dir(match)), "postgresql"))
		if err != nil || major < serverMajor {
			continue
		}
		if best == "" || major < bestMajor {
			best, bestMajor = match, major
		}
	}
	if best != "" {
		return best
	}
	return name
}

// pgEnv passes the connection in the environment rather than on the
// command line, where the password would show in the process list.
func pgEnv(databaseURL string) ([]string, string, error) {
	config, err := pgconn.ParseConfig(databaseURL)
	if err != nil {
		return nil, "", fmt.Errorf("DATABASE_URL 无效：%w", err)
	}
	env := append(os.Environ(),
		"PGHOST="+config.Host, "PGPORT="+strconv.Itoa(int(config.Port)),
		"PGUSER="+config.User, "PGPASSWORD="+config.Password, "PGDATABASE="+config.Database,
		"PGCONNECT_TIMEOUT=15",
	)
	sslmode := "prefer"
	if parsed, err := url.Parse(databaseURL); err == nil && parsed.Query().Get("sslmode") != "" {
		sslmode = parsed.Query().Get("sslmode")
	} else if config.TLSConfig == nil {
		sslmode = "disable"
	}
	env = append(env, "PGSSLMODE="+sslmode)
	return env, config.Database, nil
}

func runTool(ctx context.Context, program string, env []string, args ...string) error {
	command := exec.CommandContext(ctx, program, args...)
	command.Env = env
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		text := strings.TrimSpace(output.String())
		if len(text) > 600 {
			text = "…" + text[len(text)-600:]
		}
		if text == "" {
			text = err.Error()
		}
		return fmt.Errorf("%s: %s", filepath.Base(program), text)
	}
	return nil
}

// pgDump exports the whole database except the backup settings and
// history, in pg_dump's compressed custom format.
func pgDump(ctx context.Context, databaseURL string, serverMajor int, target string) error {
	env, _, err := pgEnv(databaseURL)
	if err != nil {
		return err
	}
	return runTool(ctx, pgTool("pg_dump", serverMajor), env,
		"--format=custom", "--compress=6", "--no-owner", "--no-privileges",
		"--exclude-table=public.backup_*", "--file="+target)
}

// clearSchema drops everything the platform keeps in the public schema
// except the backup tables and what extensions own. pg_restore --clean
// would only drop what the dump has: a backup from an older release would
// leave newer tables behind, still pointing at the ones being replaced.
const clearSchema = `DO $$
DECLARE item record;
BEGIN
  FOR item IN
    SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S') AND c.relname NOT LIKE 'backup\_%'
      AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
    ORDER BY CASE c.relkind WHEN 'v' THEN 0 WHEN 'm' THEN 0 WHEN 'S' THEN 2 ELSE 1 END
  LOOP
    EXECUTE format('DROP %s IF EXISTS public.%I CASCADE',
      CASE item.relkind WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' WHEN 'f' THEN 'FOREIGN TABLE' WHEN 'S' THEN 'SEQUENCE' ELSE 'TABLE' END,
      item.relname);
  END LOOP;
  FOR item IN
    SELECT p.oid::regprocedure AS signature FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'public' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
  LOOP
    EXECUTE 'DROP FUNCTION IF EXISTS ' || item.signature || ' CASCADE';
  END LOOP;
  FOR item IN
    SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
    WHERE n.nspname = 'public' AND t.typtype IN ('e', 'd', 'r') AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e')
  LOOP
    EXECUTE format('DROP TYPE IF EXISTS public.%I CASCADE', item.typname);
  END LOOP;
END $$;`

// pgRestore replaces the platform's data with the dump in one transaction:
// clear the schema, then run the dump as SQL, so a failure anywhere leaves
// the database as it was. pg_restore writes the whole script to a file
// first: fed through a pipe, a pg_restore that died half way would look to
// psql like a short script, and psql would commit it.
func pgRestore(ctx context.Context, databaseURL string, serverMajor int, dump string) error {
	env, _, err := pgEnv(databaseURL)
	if err != nil {
		return err
	}
	script := dump + ".sql"
	defer os.Remove(script)
	if err := runTool(ctx, pgTool("pg_restore", serverMajor), env, "--no-owner", "--no-privileges", "--file="+script, dump); err != nil {
		return err
	}
	return runTool(ctx, pgTool("psql", serverMajor), env, "--no-psqlrc", "--quiet", "--single-transaction",
		"--set=ON_ERROR_STOP=1", "--command="+clearSchema, "--file="+script)
}
