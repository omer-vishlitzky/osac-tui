package client

import (
	"context"
	"net"
	"sort"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestResourcesIncludesAllPublicEntities(t *testing.T) {
	expectedWritable := []string{
		"baremetalinstancecatalogitems",
		"baremetalinstancetemplates",
		"baremetalinstances",
		"clustercatalogitems",
		"clustertemplates",
		"clusterversions",
		"clusters",
		"computeinstancecatalogitems",
		"computeinstancetemplates",
		"computeinstances",
		"diskimages",
		"externalipattachments",
		"externalips",
		"hosttypes",
		"identityproviders",
		"instancetypes",
		"natgateways",
		"projectmemberships",
		"projects",
		"rolebindings",
		"roles",
		"secrets",
		"securitygroups",
		"subnets",
		"tenants",
		"users",
		"virtualnetworks",
	}
	expectedReadOnly := []string{
		"addonoperators",
		"baremetalinstancetypes",
		"externalippools",
		"storagetiers",
		"volumes",
	}

	var serviceNames []string
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if string(file.Package()) != "osac.public.v1" {
			return true
		}
		for index := 0; index < file.Services().Len(); index++ {
			service := file.Services().Get(index)
			if isEntityService(service) {
				serviceNames = append(serviceNames, string(service.FullName()))
			}
		}
		return true
	})
	resources := resourcesFromServices(serviceNames, new(protoregistry.Files), nil, false)
	if len(resources) != len(expectedWritable)+len(expectedReadOnly) {
		t.Fatalf("got %d resources, want %d", len(resources), len(expectedWritable)+len(expectedReadOnly))
	}

	for _, key := range expectedWritable {
		resource, ok := resources[key]
		if !ok {
			t.Errorf("missing writable resource %q", key)
			continue
		}
		if !resource.Writable() {
			t.Errorf("resource %q is unexpectedly read-only", key)
		}
	}
	for _, key := range expectedReadOnly {
		resource, ok := resources[key]
		if !ok {
			t.Errorf("missing read-only resource %q", key)
			continue
		}
		if resource.Writable() {
			t.Errorf("resource %q is unexpectedly writable", key)
		}
	}

	keys := resourceOrder(resources)
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("resource order is not sorted: %v", keys)
	}
}

func TestDiscoverResourcesUsesEndpointServices(t *testing.T) {
	tests := []struct {
		name          string
		services      []string
		unimplemented []string
		denied        []string
		privateAPI    bool
		want          []string
	}{
		{
			name:     "public endpoint",
			services: []string{"osac.public.v1.Projects", "osac.public.v1.ConsoleProxy"},
			want:     []string{"projects"},
		},
		{
			name:       "private endpoint",
			services:   []string{"osac.private.v1.StorageBackends", "osac.private.v1.StorageTiers"},
			privateAPI: true,
			want:       []string{"storagebackends", "storagetiers"},
		},
		{
			name:       "private selection excludes public resources",
			services:   []string{"osac.public.v1.Projects", "osac.private.v1.StorageBackends"},
			privateAPI: true,
			want:       []string{"storagebackends"},
		},
		{
			name:     "public selection excludes private resources",
			services: []string{"osac.public.v1.Projects", "osac.private.v1.StorageBackends"},
			want:     []string{"projects"},
		},
		{
			name:          "omits reflected but unimplemented private services",
			services:      []string{"osac.private.v1.StorageBackends", "osac.private.v1.StorageTiers"},
			unimplemented: []string{"osac.private.v1.StorageBackends"},
			privateAPI:    true,
			want:          []string{"storagetiers"},
		},
		{
			name:       "omits private services denied to the caller",
			services:   []string{"osac.private.v1.StorageBackends", "osac.private.v1.StorageTiers"},
			denied:     []string{"osac.private.v1.StorageBackends"},
			privateAPI: true,
			want:       []string{"storagetiers"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := discoverTestResources(t, test.services, test.unimplemented, test.denied, test.privateAPI)
			keys := resourceOrder(resources)
			if len(keys) != len(test.want) {
				t.Fatalf("resource keys = %v, want %v", keys, test.want)
			}
			for index := range keys {
				if keys[index] != test.want[index] {
					t.Fatalf("resource keys = %v, want %v", keys, test.want)
				}
			}
		})
	}
}

func discoverTestResources(t *testing.T, serviceNames, unimplemented, denied []string, privateAPI bool) map[string]Resource {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	unimplementedServices := make(map[string]bool, len(unimplemented))
	for _, name := range unimplemented {
		unimplementedServices[name] = true
	}
	deniedServices := make(map[string]bool, len(denied))
	for _, name := range denied {
		deniedServices[name] = true
	}
	for _, name := range serviceNames {
		service := &grpc.ServiceDesc{
			ServiceName: name,
			HandlerType: (*testRegisteredService)(nil),
		}
		if !unimplementedServices[name] {
			if method := testListMethod(name, deniedServices[name]); method != nil {
				service.Methods = []grpc.MethodDesc{*method}
			}
		}
		server.RegisterService(service, testRegisteredServiceImpl{})
	}
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial test reflection server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	resources, err := discoverResources(ctx, conn, privateAPI)
	if err != nil {
		t.Fatalf("discover resources: %v", err)
	}
	return resources
}

func testListMethod(serviceName string, denied bool) *grpc.MethodDesc {
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return nil
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil
	}
	method := service.Methods().ByName("List")
	if method == nil {
		return nil
	}
	return &grpc.MethodDesc{
		MethodName: "List",
		Handler: func(server interface{}, ctx context.Context, decode func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
			request := dynamicpb.NewMessage(method.Input())
			if err := decode(request); err != nil {
				return nil, err
			}
			handler := func(context.Context, interface{}) (interface{}, error) {
				if denied {
					return nil, status.Error(codes.PermissionDenied, "not allowed")
				}
				return dynamicpb.NewMessage(method.Output()), nil
			}
			if interceptor == nil {
				return handler(ctx, request)
			}
			return interceptor(ctx, request, &grpc.UnaryServerInfo{
				Server:     server,
				FullMethod: "/" + serviceName + "/List",
			}, handler)
		},
	}
}

type testRegisteredService interface {
	testRegisteredService()
}

type testRegisteredServiceImpl struct{}

func (testRegisteredServiceImpl) testRegisteredService() {}

func TestListFilterScopesTenant(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		options ListOptions
		want    string
	}{
		{
			name:    "resource tenant",
			key:     "projects",
			options: ListOptions{Tenant: "team-a"},
			want:    `this.metadata.tenant == "team-a"`,
		},
		{
			name:    "tenant resource",
			key:     "tenants",
			options: ListOptions{Tenant: "team-a"},
			want:    `this.metadata.name == "team-a"`,
		},
		{
			name:    "combines filters",
			key:     "projects",
			options: ListOptions{Tenant: "team-a", Filter: `this.metadata.name.startsWith("prod")`},
			want:    `(this.metadata.tenant == "team-a") && (this.metadata.name.startsWith("prod"))`,
		},
		{
			name:    "escapes tenant literal",
			key:     "projects",
			options: ListOptions{Tenant: `team" || true || "x`},
			want:    `this.metadata.tenant == "team\" || true || \"x"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := listFilter(test.key, test.options); got != test.want {
				t.Errorf("listFilter() = %q, want %q", got, test.want)
			}
		})
	}
}
