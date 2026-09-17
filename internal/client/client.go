package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
	Tenant  string
	Created time.Time
	Version string
}

type ListOptions struct {
	Offset int
	Limit  int
	Filter string
	Order  string
}

type ListResult struct {
	Rows  []Row
	Total int
}

type Resource interface {
	Key() string
	Title() string
	List(context.Context, ListOptions) (ListResult, error)
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
	transport credentials.TransportCredentials
	secure    bool
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
		transport: transport,
		secure:    config.TLSConfig != nil,
	}, nil
}

type SerialConsole struct {
	conn   *grpc.ClientConn
	stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse]
	cancel context.CancelFunc
}

func (c *Client) OpenSerialConsole(ctx context.Context, resourceType publicv1.ConsoleResourceType, resourceID string) (*SerialConsole, error) {
	session, err := publicv1.NewConsoleSessionsClient(c.conn).Create(ctx, (&publicv1.ConsoleSessionsCreateRequest_builder{
		Object: (&publicv1.ConsoleSession_builder{
			ResourceType: resourceType,
			ResourceId:   resourceID,
			Type:         publicv1.ConsoleType_CONSOLE_TYPE_SERIAL,
			ClientId:     fmt.Sprintf("osac-tui-%d", os.Getpid()),
		}).Build(),
	}).Build())
	if err != nil {
		return nil, err
	}
	if session.GetObject() == nil {
		return nil, fmt.Errorf("console session response contains no object")
	}
	ticket := session.GetObject().GetTicket()
	if ticket == "" {
		return nil, fmt.Errorf("console session response contains no ticket")
	}
	consoleConn, err := grpc.DialContext(ctx, c.info.Address,
		grpc.WithTransportCredentials(c.transport),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to console proxy: %w", err)
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	stream, err := publicv1.NewConsoleProxyClient(consoleConn).Connect(streamCtx,
		grpc.PerRPCCredentials(consoleCredentials{token: ticket, secure: c.secure}),
	)
	if err != nil {
		cancel()
		_ = consoleConn.Close()
		return nil, fmt.Errorf("open serial console: %w", err)
	}
	return &SerialConsole{conn: consoleConn, stream: stream, cancel: cancel}, nil
}

func (c *SerialConsole) Send(data []byte) error {
	return c.stream.Send((&publicv1.ConsoleProxyConnectRequest_builder{
		Input: (&publicv1.ConsoleInput_builder{Data: data}).Build(),
	}).Build())
}

func (c *SerialConsole) Receive() ([]byte, string, error) {
	response, err := c.stream.Recv()
	if err != nil {
		return nil, "", err
	}
	if output := response.GetOutput(); output != nil {
		return output.GetData(), "", nil
	}
	if status := response.GetStatus(); status != nil {
		return nil, status.GetState().String() + ": " + status.GetMessage(), nil
	}
	return nil, "", nil
}

func (c *SerialConsole) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	return c.conn.Close()
}

type consoleCredentials struct {
	token  string
	secure bool
}

func (c consoleCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + c.token}, nil
}

func (c consoleCredentials) RequireTransportSecurity() bool { return c.secure }

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
