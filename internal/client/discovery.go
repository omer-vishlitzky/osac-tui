package client

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func discoverResources(ctx context.Context, conn *grpc.ClientConn, privateAPI bool) (map[string]Resource, error) {
	stream, err := reflectionv1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("open gRPC reflection stream: %w", err)
	}
	defer stream.CloseSend()

	response, err := reflectionRequest(stream, &reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
	})
	if err != nil {
		return nil, fmt.Errorf("list endpoint services: %w", err)
	}
	serviceList := response.GetListServicesResponse()
	if serviceList == nil {
		return nil, fmt.Errorf("reflection returned no service list")
	}

	serviceNames := make([]string, 0, len(serviceList.GetService()))
	reflectedFiles := make(map[string]*descriptorpb.FileDescriptorProto)
	servicePackage := "osac.public.v1."
	if privateAPI {
		servicePackage = "osac.private.v1."
	}
	for _, service := range serviceList.GetService() {
		name := service.GetName()
		if !strings.HasPrefix(name, servicePackage) {
			continue
		}
		serviceNames = append(serviceNames, name)

		response, err := reflectionRequest(stream, &reflectionv1.ServerReflectionRequest{
			MessageRequest: &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: name},
		})
		if err != nil {
			if _, localErr := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name)); localErr == nil {
				continue
			}
			return nil, fmt.Errorf("load descriptor for service %s: %w", name, err)
		}
		fileResponse := response.GetFileDescriptorResponse()
		if fileResponse == nil {
			return nil, fmt.Errorf("reflection returned no file descriptor for service %s", name)
		}
		for _, data := range fileResponse.GetFileDescriptorProto() {
			file := new(descriptorpb.FileDescriptorProto)
			if err := proto.Unmarshal(data, file); err != nil {
				return nil, fmt.Errorf("decode descriptor for service %s: %w", name, err)
			}
			if file.GetName() != "" {
				reflectedFiles[file.GetName()] = file
			}
		}
	}

	files, err := loadReflectedFiles(reflectedFiles)
	if err != nil {
		return nil, err
	}
	resources := resourcesFromServices(serviceNames, files, conn, privateAPI)
	if len(resources) == 0 {
		return nil, fmt.Errorf("endpoint exposes no supported listable %s API services", apiName(privateAPI))
	}
	var probeSummary listProbeSummary
	resources, probeSummary, err = probeResourceLists(ctx, resources)
	if err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("endpoint exposes no accessible listable %s API services (%d unimplemented, %d permission denied)", apiName(privateAPI), probeSummary.unimplemented, probeSummary.permissionDenied)
	}
	return resources, nil
}

func apiName(privateAPI bool) string {
	if privateAPI {
		return "private"
	}
	return "public"
}

type listProbeSummary struct {
	unimplemented    int
	permissionDenied int
}

func probeResourceLists(ctx context.Context, resources map[string]Resource) (map[string]Resource, listProbeSummary, error) {
	type probeResult struct {
		key string
		err error
	}

	keys := resourceOrder(resources)
	workerCount := len(keys)
	if workerCount > 8 {
		workerCount = 8
	}
	jobs := make(chan string)
	results := make(chan probeResult, len(keys))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for key := range jobs {
				_, err := resources[key].List(ctx, ListOptions{Limit: 1})
				results <- probeResult{key: key, err: err}
			}
		}()
	}
	go func() {
		for _, key := range keys {
			jobs <- key
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	accessible := make(map[string]Resource, len(resources))
	var summary listProbeSummary
	for result := range results {
		if result.err == nil {
			accessible[result.key] = resources[result.key]
			continue
		}
		switch status.Code(result.err) {
		case codes.Unimplemented:
			summary.unimplemented++
		case codes.PermissionDenied:
			summary.permissionDenied++
			continue
		default:
			return nil, summary, fmt.Errorf("check access to %s: %w", result.key, result.err)
		}
	}
	return accessible, summary, nil
}

func reflectionRequest(stream grpc.BidiStreamingClient[reflectionv1.ServerReflectionRequest, reflectionv1.ServerReflectionResponse], request *reflectionv1.ServerReflectionRequest) (*reflectionv1.ServerReflectionResponse, error) {
	if err := stream.Send(request); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if reflectionError := response.GetErrorResponse(); reflectionError != nil {
		return nil, fmt.Errorf("reflection error %d: %s", reflectionError.GetErrorCode(), reflectionError.GetErrorMessage())
	}
	return response, nil
}

func loadReflectedFiles(descriptors map[string]*descriptorpb.FileDescriptorProto) (*protoregistry.Files, error) {
	files := new(protoregistry.Files)
	pending := make([]*descriptorpb.FileDescriptorProto, 0, len(descriptors))
	for _, descriptor := range descriptors {
		pending = append(pending, descriptor)
	}
	for len(pending) > 0 {
		remaining := pending[:0]
		progress := false
		var lastError error
		for _, descriptor := range pending {
			resolver := reflectedDescriptorResolver{files: files}
			file, err := (protodesc.FileOptions{}).New(descriptor, resolver)
			if err != nil {
				remaining = append(remaining, descriptor)
				lastError = err
				continue
			}
			if err := files.RegisterFile(file); err != nil {
				return nil, fmt.Errorf("register reflected file %q: %w", descriptor.GetName(), err)
			}
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("load reflected protobuf descriptors: %w", lastError)
		}
		pending = remaining
	}
	return files, nil
}

type reflectedDescriptorResolver struct {
	files *protoregistry.Files
}

func (r reflectedDescriptorResolver) FindFileByPath(path string) (protoreflect.FileDescriptor, error) {
	if file, err := r.files.FindFileByPath(path); err == nil {
		return file, nil
	}
	return protoregistry.GlobalFiles.FindFileByPath(path)
}

func (r reflectedDescriptorResolver) FindDescriptorByName(name protoreflect.FullName) (protoreflect.Descriptor, error) {
	if descriptor, err := r.files.FindDescriptorByName(name); err == nil {
		return descriptor, nil
	}
	return protoregistry.GlobalFiles.FindDescriptorByName(name)
}

func resourcesFromServices(names []string, files *protoregistry.Files, conn *grpc.ClientConn, privateAPI bool) map[string]Resource {
	result := make(map[string]Resource)
	servicePackage := "osac.public.v1."
	if privateAPI {
		servicePackage = "osac.private.v1."
	}
	for _, name := range names {
		if !strings.HasPrefix(name, servicePackage) {
			continue
		}
		descriptor, err := files.FindDescriptorByName(protoreflect.FullName(name))
		if err != nil {
			descriptor, err = protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
		}
		if err != nil {
			continue
		}
		service, ok := descriptor.(protoreflect.ServiceDescriptor)
		if !ok || !isEntityService(service) {
			continue
		}
		resource := newResource(conn, service)
		result[resource.Key()] = resource
	}
	return result
}

func isEntityService(service protoreflect.ServiceDescriptor) bool {
	listMethod := service.Methods().ByName("List")
	getMethod := service.Methods().ByName("Get")
	if listMethod == nil || getMethod == nil {
		return false
	}

	idField := getMethod.Input().Fields().ByName("id")
	if idField == nil || idField.Kind() != protoreflect.StringKind {
		return false
	}
	objectField := getMethod.Output().Fields().ByName("object")
	itemsField := listMethod.Output().Fields().ByName("items")
	if objectField == nil || objectField.Kind() != protoreflect.MessageKind ||
		itemsField == nil || itemsField.Cardinality() != protoreflect.Repeated || itemsField.Kind() != protoreflect.MessageKind {
		return false
	}
	return objectField.Message().FullName() == itemsField.Message().FullName()
}
