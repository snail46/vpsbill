// Command hatch-agent runs on a host node and connects it to VPSBill.
//
//	hatch-agent init --server https://billing.example.com --runtime lxd,podman --public-ip 203.0.113.10
//	hatch-agent run
//	hatch-agent token
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"vpsbill/internal/hatch/agent"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	command := "run"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	var err error
	switch command {
	case "init":
		err = initConfig(args)
	case "run":
		err = run(args)
	case "token":
		err = printToken(args)
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (use init, run, token or version)", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hatch-agent:", err)
		os.Exit(1)
	}
}

func initConfig(args []string) error {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	path := flags.String("config", agent.DefaultConfigPath, "config file to create")
	server := flags.String("server", "", "billing site URL, e.g. https://billing.example.com")
	runtimes := flags.String("runtime", "lxd", "comma separated runtimes: lxd (or incus), podman")
	publicIP := flags.String("public-ip", "", "public IPv4 shown for NAT port forwards")
	force := flags.Bool("force", false, "overwrite an existing config and rotate the token")
	lxdNetwork := flags.String("lxd-network", "", "LXD/Incus bridge for instances (default lxdbr0 or incusbr0)")
	podmanNetwork := flags.String("podman-network", "", "Podman network for instances (default podman)")
	_ = flags.Parse(args)
	if _, err := os.Stat(*path); err == nil && !*force {
		return fmt.Errorf("%s exists; use --force to rotate the token", *path)
	}
	token, err := agent.NewToken()
	if err != nil {
		return err
	}
	config := agent.Config{ServerURL: *server, Token: token, PublicIPv4: *publicIP, PortRangeStart: 20000, PortRangeEnd: 60000, StateDir: "/var/lib/hatch"}
	for _, runtime := range strings.Split(*runtimes, ",") {
		switch strings.TrimSpace(runtime) {
		case "lxd":
			config.LXD = &agent.LXDConfig{Network: *lxdNetwork}
		case "incus":
			// Pin the socket: hosts may run the LXD snap next to Incus.
			config.LXD = &agent.LXDConfig{Socket: "/var/lib/incus/unix.socket", Network: *lxdNetwork}
		case "podman":
			config.Podman = &agent.PodmanConfig{Network: *podmanNetwork}
		case "":
		default:
			return fmt.Errorf("unknown runtime %q", runtime)
		}
	}
	config.ApplyDefaults()
	if err := config.Validate(); err != nil {
		return err
	}
	if err := agent.WriteConfig(*path, config); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n\nAdd this node in VPSBill (对接方式: Hatch Agent) with the token:\n\n  %s\n\n", *path, token)
	return nil
}

func printToken(args []string) error {
	flags := flag.NewFlagSet("token", flag.ExitOnError)
	path := flags.String("config", agent.DefaultConfigPath, "config file")
	_ = flags.Parse(args)
	config, err := agent.LoadConfig(*path)
	if err != nil {
		return err
	}
	fmt.Println(config.Token)
	return nil
}

func run(args []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	path := flags.String("config", agent.DefaultConfigPath, "config file")
	_ = flags.Parse(args)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := agent.LoadConfig(*path)
	if err != nil {
		return err
	}
	store, err := agent.OpenStore(config.StateDir)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	var runtimes []agent.Runtime
	if config.LXD != nil {
		runtimes = append(runtimes, agent.NewLXD(*config.LXD))
	}
	if config.Podman != nil {
		runtimes = append(runtimes, agent.NewPodman(*config.Podman))
	}
	service := agent.NewService(config, version, store, runtimes, agent.NFT{}, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := service.Init(ctx); err != nil && !errors.Is(err, context.Canceled) {
		// Keep running: the server can still manage instances, and the next
		// port-forward change re-applies the full rule set.
		logger.Error("restore port forwards", "error", err)
	}
	client, err := agent.NewClient(config, version, service, logger)
	if err != nil {
		return err
	}
	go service.Meter(ctx, time.Minute)
	attrs := []any{"version", version, "runtimes", config.Runtimes()}
	if config.LXD != nil {
		attrs = append(attrs, "lxd_socket", config.LXD.Socket, "lxd_network", config.LXD.Network)
	}
	if config.Podman != nil {
		attrs = append(attrs, "podman_network", config.Podman.Network)
	}
	logger.Info("hatch agent started", attrs...)
	client.Run(ctx)
	return nil
}
