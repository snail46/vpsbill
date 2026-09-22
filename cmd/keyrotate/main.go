package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"clicd-billing/internal/security"
	"github.com/jackc/pgx/v5"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	oldBox, err := security.NewSecretBox(os.Getenv("OLD_ENCRYPTION_KEY"))
	if err != nil {
		log.Fatal("invalid old encryption key")
	}
	newBox, err := security.NewSecretBox(os.Getenv("NEW_ENCRYPTION_KEY"))
	if err != nil {
		log.Fatal("invalid new encryption key")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		log.Fatal("database connection failed: ", err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Rollback(ctx)
	count := 0
	rows, err := tx.Query(ctx, `SELECT id,api_key_ciphertext FROM nodes FOR UPDATE`)
	if err != nil {
		log.Fatal(err)
	}
	type secretRow struct {
		id    string
		value []byte
	}
	values := []secretRow{}
	for rows.Next() {
		var row secretRow
		if err := rows.Scan(&row.id, &row.value); err != nil {
			log.Fatal(err)
		}
		values = append(values, row)
	}
	rows.Close()
	for _, row := range values {
		plain, err := oldBox.Open(row.value)
		if err != nil {
			log.Fatalf("cannot decrypt node %s; rotation aborted", row.id)
		}
		sealed, err := newBox.Seal(plain)
		if err != nil {
			log.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE nodes SET api_key_ciphertext=$2,updated_at=now() WHERE id=$1`, row.id, sealed); err != nil {
			log.Fatal(err)
		}
		count++
	}
	userRows, err := tx.Query(ctx, `SELECT id,mfa_secret_encrypted,mfa_pending_secret_encrypted FROM users WHERE mfa_secret_encrypted IS NOT NULL OR mfa_pending_secret_encrypted IS NOT NULL FOR UPDATE`)
	if err != nil {
		log.Fatal(err)
	}
	type userSecret struct {
		id              string
		active, pending []byte
	}
	users := []userSecret{}
	for userRows.Next() {
		var row userSecret
		if err := userRows.Scan(&row.id, &row.active, &row.pending); err != nil {
			log.Fatal(err)
		}
		users = append(users, row)
	}
	userRows.Close()
	for _, row := range users {
		active, err := reencrypt(oldBox, newBox, row.active)
		if err != nil {
			log.Fatalf("cannot decrypt MFA secret for user %s; rotation aborted", row.id)
		}
		pending, err := reencrypt(oldBox, newBox, row.pending)
		if err != nil {
			log.Fatalf("cannot decrypt pending MFA secret for user %s; rotation aborted", row.id)
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET mfa_secret_encrypted=$2,mfa_pending_secret_encrypted=$3 WHERE id=$1`, row.id, active, pending); err != nil {
			log.Fatal(err)
		}
		count++
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,action,target_type,metadata) VALUES('system','encryption_key.rotated','system',jsonb_build_object('records',$1))`, count); err != nil {
		log.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("rotated %d encrypted records\n", count)
}

func reencrypt(oldBox, newBox *security.SecretBox, value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	plain, err := oldBox.Open(value)
	if err != nil {
		return nil, err
	}
	return newBox.Seal(plain)
}
