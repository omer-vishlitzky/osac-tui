package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Address   string
	TLSConfig *tls.Config
	Token     string
}

type ConnectionInfo struct {
	Address      string
	User         string
	Organization string
}

type Row struct {
	Object  proto.Message
	ID      string
	Name    string
	State   string
	Version string
}

type Resource interface {
	Key() string
	Title() string
	List(context.Context) ([]Row, error)
	Get(context.Context, string) (proto.Message, error)
	Create(context.Context, proto.Message) (proto.Message, error)
	Update(context.Context, proto.Message) (proto.Message, error)
	Delete(context.Context, string) error
	New() proto.Message
	Row(proto.Message) Row
	Writable() bool
}

type Client struct {
	conn      *grpc.ClientConn
	resources map[string]Resource
	order     []string
	info      ConnectionInfo
}

func Dial(ctx context.Context, config Config) (*Client, error) {
	transport := credentials.TransportCredentials(insecure.NewCredentials())
	if config.TLSConfig != nil {
		transport = credentials.NewTLS(config.TLSConfig)
	}

	options := []grpc.DialOption{
		grpc.WithTransportCredentials(transport),
	}
	if config.Token != "" {
		options = append(options, grpc.WithUnaryInterceptor(bearerInterceptor(config.Token)))
	}

	conn, err := grpc.DialContext(ctx, config.Address, options...)
	if err != nil {
		return nil, fmt.Errorf("connect to fulfillment-service at %q: %w", config.Address, err)
	}

	resources := resources(conn)
	return &Client{
		conn:      conn,
		resources: resources,
		order:     resourceOrder(resources),
		info:      connectionInfo(config.Address, config.Token),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Resource(key string) Resource {
	return c.resources[key]
}

func (c *Client) Resources() []Resource {
	result := make([]Resource, 0, len(c.order))
	for _, key := range c.order {
		result = append(result, c.resources[key])
	}
	return result
}

func (c *Client) ConnectionInfo() ConnectionInfo { return c.info }

func connectionInfo(address, token string) ConnectionInfo {
	info := ConnectionInfo{Address: address, User: "anonymous"}
	if token == "" {
		return info
	}

	info.User = "authenticated"
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return info
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return info
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return info
	}
	for _, key := range []string{"preferred_username", "username", "sub"} {
		if value, ok := claims[key].(string); ok && value != "" {
			info.User = value
			break
		}
	}
	if organization, ok := claims["organization"].(map[string]any); ok {
		organizations := make([]string, 0, len(organization))
		for name := range organization {
			organizations = append(organizations, name)
		}
		sort.Strings(organizations)
		info.Organization = strings.Join(organizations, ", ")
	}
	return info
}

func bearerInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, reply interface{}, conn *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, request, reply, conn, options...)
	}
}

func resourceOrder(resources map[string]Resource) []string {
	keys := make([]string, 0, len(resources))
	for key := range resources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
