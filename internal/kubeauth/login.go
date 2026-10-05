package kubeauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultNamespace      = "osac"
	defaultServiceAccount = "admin"
)

// Result contains the connection details discovered from the selected cluster.
type Result struct {
	Address string
	Token   string
	CAPEM   []byte
}

type resourceList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Hostnames []string `json:"hostnames"`
			Rules     []struct {
				Host string `json:"host"`
			} `json:"rules"`
			Host string `json:"host"`
		} `json:"spec"`
	} `json:"items"`
}

type endpoint struct {
	name string
	host string
}

type kubectlRunner func(context.Context, string, ...string) ([]byte, error)

// Login discovers the OSAC API endpoint and, when requested, creates a token
// for the admin ServiceAccount using the selected kubeconfig.
func Login(ctx context.Context, kubeconfig string, needAddress, needToken bool) (Result, error) {
	namespace := envOr("OSAC_NAMESPACE", defaultNamespace)
	serviceAccount := envOr("OSAC_SERVICE_ACCOUNT", defaultServiceAccount)

	var result Result
	if needAddress {
		apiEndpoints, err := discoverEndpoints(ctx, kubeconfig, namespace)
		if err != nil {
			return Result{}, fmt.Errorf("discover OSAC endpoint: %w", err)
		}
		apiHost, err := selectEndpoint(apiEndpoints)
		if err != nil {
			return Result{}, fmt.Errorf("find the OSAC API address in namespace %q: %w", namespace, err)
		}
		address, err := endpointAddress(apiHost)
		if err != nil {
			return Result{}, err
		}
		result.Address = address
	}

	result.CAPEM = clusterCA(ctx, kubeconfig, namespace)
	if !needToken {
		return result, nil
	}

	token, err := createServiceAccountToken(ctx, kubeconfig, namespace, serviceAccount, kubectl)
	if err != nil {
		return Result{}, err
	}
	result.Token = token
	return result, nil
}

func endpointAddress(host string) (string, error) {
	port := strings.TrimSpace(os.Getenv("OSAC_GATEWAY_PORT"))
	if port == "" {
		port = "443"
		if strings.HasSuffix(strings.ToLower(host), ".osac.localhost") {
			port = "8443"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("invalid OSAC_GATEWAY_PORT %q: expected a port from 1 to 65535", port)
	}
	return net.JoinHostPort(host, strconv.Itoa(portNumber)), nil
}

func createServiceAccountToken(ctx context.Context, kubeconfig, namespace, serviceAccount string, runKubectl kubectlRunner) (string, error) {
	output, err := runKubectl(ctx, kubeconfig, "-n", namespace, "create", "token", serviceAccount)
	if err != nil {
		return "", fmt.Errorf("create token for ServiceAccount %s/%s: %w", namespace, serviceAccount, err)
	}
	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("create token for ServiceAccount %s/%s: kubectl returned an empty token", namespace, serviceAccount)
	}
	return token, nil
}

func discoverEndpoints(ctx context.Context, kubeconfig, namespace string) ([]endpoint, error) {
	var endpoints []endpoint
	var failures []error
	succeeded := false
	for _, kind := range []string{
		"ingress",
		"httproutes.gateway.networking.k8s.io",
		"tlsroutes.gateway.networking.k8s.io",
		"routes.route.openshift.io",
	} {
		data, err := kubectl(ctx, kubeconfig, "-n", namespace, "get", kind, "-o", "json")
		if err != nil {
			failures = append(failures, fmt.Errorf("get %s: %w", kind, err))
			continue
		}
		succeeded = true
		found, err := parseResourceEndpoints(data)
		if err != nil {
			failures = append(failures, fmt.Errorf("decode %s: %w", kind, err))
			continue
		}
		endpoints = append(endpoints, found...)
	}
	if !succeeded {
		return nil, errors.Join(failures...)
	}
	return endpoints, nil
}

func parseResourceEndpoints(data []byte) ([]endpoint, error) {
	var list resourceList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	var endpoints []endpoint
	for _, item := range list.Items {
		hosts := append([]string(nil), item.Spec.Hostnames...)
		for _, rule := range item.Spec.Rules {
			hosts = append(hosts, rule.Host)
		}
		hosts = append(hosts, item.Spec.Host)
		for _, host := range hosts {
			host = strings.TrimSpace(host)
			if host != "" {
				endpoints = append(endpoints, endpoint{name: item.Metadata.Name, host: host})
			}
		}
	}
	return endpoints, nil
}

func selectEndpoint(endpoints []endpoint) (string, error) {
	if len(endpoints) == 0 {
		return "", errors.New("no hostname found")
	}
	type rankedEndpoint struct {
		endpoint
		rank int
	}
	ranked := make([]rankedEndpoint, 0, len(endpoints))
	for _, candidate := range endpoints {
		rank := 0
		name := strings.ToLower(candidate.name)
		host := strings.ToLower(candidate.host)
		if strings.Contains(name, "osac-ui") || strings.Contains(host, "osac-ui") {
			continue
		}
		switch {
		case strings.Contains(name, "fulfillment-api"):
			rank = 100
		case strings.Contains(host, "fulfillment-api"):
			rank = 95
		case strings.Contains(name, "osac-api"):
			rank = 90
		case strings.Contains(host, "osac-api"):
			rank = 85
		case strings.Contains(name, "osac"):
			rank = 60
		case strings.Contains(host, "osac"):
			rank = 50
		}
		if rank > 0 {
			ranked = append(ranked, rankedEndpoint{endpoint: candidate, rank: rank})
		}
	}
	if len(ranked) == 0 {
		var remaining []endpoint
		for _, candidate := range endpoints {
			if !strings.Contains(strings.ToLower(candidate.name), "osac-ui") && !strings.Contains(strings.ToLower(candidate.host), "osac-ui") {
				remaining = append(remaining, candidate)
			}
		}
		if len(remaining) == 1 {
			return remaining[0].host, nil
		}
	}
	if len(ranked) == 0 {
		return "", errors.New("no matching hostname found")
	}
	sort.SliceStable(ranked, func(left, right int) bool { return ranked[left].rank > ranked[right].rank })
	if len(ranked) > 1 && ranked[0].rank == ranked[1].rank && ranked[0].host != ranked[1].host {
		return "", fmt.Errorf("multiple matching hostnames found (%q and %q)", ranked[0].host, ranked[1].host)
	}
	return ranked[0].host, nil
}

func clusterCA(ctx context.Context, kubeconfig, namespace string) []byte {
	for _, target := range []struct {
		kind string
		name string
		key  string
	}{{"configmap", "ca-bundle", "bundle.pem"}, {"secret", "fulfillment-api-tls", "ca.crt"}} {
		data, err := kubectl(ctx, kubeconfig, "-n", namespace, "get", target.kind, target.name, "-o", "json")
		if err != nil {
			continue
		}
		if target.kind == "configmap" {
			var object struct {
				Data map[string]string `json:"data"`
			}
			if json.Unmarshal(data, &object) == nil && object.Data[target.key] != "" {
				return []byte(object.Data[target.key])
			}
			continue
		}
		var object struct {
			Data map[string]string `json:"data"`
		}
		if json.Unmarshal(data, &object) == nil && object.Data[target.key] != "" {
			if decoded, err := base64.StdEncoding.DecodeString(object.Data[target.key]); err == nil {
				return decoded
			}
		}
	}
	return nil
}

func kubectl(ctx context.Context, kubeconfig string, args ...string) ([]byte, error) {
	commandArgs := make([]string, 0, len(args)+2)
	if strings.TrimSpace(kubeconfig) != "" {
		commandArgs = append(commandArgs, "--kubeconfig", kubeconfig)
	}
	commandArgs = append(commandArgs, args...)
	command, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(command, "kubectl", commandArgs...).Output()
	if err != nil {
		message := strings.TrimSpace(string(output))
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			if message == "" {
				message = stderr
			} else if stderr != "" {
				message += "\n" + stderr
			}
		}
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}
	return bytes.TrimSpace(output), nil
}

// DefaultKubeconfig is a convenient starting value for the login prompt. An
// empty value remains valid and lets kubectl use its normal context selection.
func DefaultKubeconfig() string {
	if configured := strings.TrimSpace(os.Getenv("KUBECONFIG")); configured != "" {
		if !strings.ContainsRune(configured, os.PathListSeparator) {
			return configured
		}
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".kube", "config")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
