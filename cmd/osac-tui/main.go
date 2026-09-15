package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/osac-project/osac-tui/internal/client"
	"github.com/osac-project/osac-tui/internal/ui"
)

type options struct {
	address  string
	tls      bool
	insecure bool
	caFile   string
	token    string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "osac-tui: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	var options options
	flag.StringVar(&options.address, "address", "localhost:8000", "fulfillment-service gRPC address")
	flag.BoolVar(&options.tls, "tls", false, "use TLS for the gRPC connection")
	flag.BoolVar(&options.insecure, "insecure", false, "skip TLS certificate verification (unsafe)")
	flag.StringVar(&options.caFile, "ca-file", "", "PEM file containing an additional CA certificate")
	flag.StringVar(&options.token, "token", "", "bearer token for the gRPC connection")
	flag.Parse()

	tlsConfig, err := loadTLSConfig(options)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	api, err := client.Dial(ctx, client.Config{
		Address:   options.address,
		TLSConfig: tlsConfig,
		Token:     options.token,
	})
	if err != nil {
		return err
	}
	defer api.Close()

	_, err = tea.NewProgram(ui.New(api), tea.WithAltScreen()).Run()
	return err
}

func loadTLSConfig(options options) (*tls.Config, error) {
	if !options.tls && options.insecure {
		return nil, errors.New("--insecure requires --tls")
	}
	if !options.tls && options.caFile != "" {
		return nil, errors.New("--ca-file requires --tls")
	}
	if !options.tls {
		return nil, nil
	}

	config := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: options.insecure,
	}
	if options.caFile == "" {
		return config, nil
	}

	data, err := os.ReadFile(options.caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file %q: %w", options.caFile, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system CA pool: %w", err)
	}
	if ok := roots.AppendCertsFromPEM(data); !ok {
		return nil, fmt.Errorf("CA file %q contains no certificates", options.caFile)
	}
	config.RootCAs = roots
	return config, nil
}
