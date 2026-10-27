package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/submon"
)

func main() {
	flags := flag.NewFlagSet("subscription-monitor", flag.ContinueOnError)
	dataDir := flags.String("data-dir", "", "Diretório absoluto e privado para histórico e chave local (obrigatório)")
	listen := flags.String("listen", "127.0.0.1:8318", "Endereço de loopback do painel")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(2)
	}
	if *dataDir == "" || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Informe --data-dir com um diretório absoluto para o monitor.")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := submon.Run(ctx, submon.Options{DataDir: *dataDir, ListenAddr: *listen}); err != nil {
		fmt.Fprintf(os.Stderr, "Falha no monitor: %v\n", err)
		os.Exit(1)
	}
}
