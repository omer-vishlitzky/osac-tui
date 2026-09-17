package client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

var errReadOnly = errors.New("resource is read-only")

type resource struct {
	key      string
	title    string
	writable bool
	list     func(context.Context, ListOptions) (ListResult, error)
	get      func(context.Context, string) (proto.Message, error)
	create   func(context.Context, proto.Message) (proto.Message, error)
	update   func(context.Context, proto.Message) (proto.Message, error)
	delete   func(context.Context, string) error
	new      func() proto.Message
	row      func(proto.Message) Row
}

func (r resource) Key() string { return r.key }

func (r resource) Title() string { return r.title }

func (r resource) Writable() bool { return r.writable }

func (r resource) List(ctx context.Context, options ListOptions) (ListResult, error) {
	return r.list(ctx, options)
}

func (r resource) Get(ctx context.Context, id string) (proto.Message, error) {
	return r.get(ctx, id)
}

func (r resource) Create(ctx context.Context, object proto.Message) (proto.Message, error) {
	return r.create(ctx, object)
}

func (r resource) Update(ctx context.Context, object proto.Message) (proto.Message, error) {
	return r.update(ctx, object)
}

func (r resource) Delete(ctx context.Context, id string) error { return r.delete(ctx, id) }

func (r resource) New() proto.Message { return r.new() }

func (r resource) Row(object proto.Message) Row { return r.row(object) }

func resources(conn *grpc.ClientConn) map[string]Resource {
	result := make(map[string]Resource)
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if string(file.Package()) != "osac.public.v1" {
			return true
		}
		for index := 0; index < file.Services().Len(); index++ {
			service := file.Services().Get(index)
			if service.Methods().ByName("List") == nil || service.Methods().ByName("Get") == nil {
				continue
			}
			resource := newResource(conn, service)
			result[resource.Key()] = resource
		}
		return true
	})
	return result
}

func newResource(conn *grpc.ClientConn, service protoreflect.ServiceDescriptor) Resource {
	listMethod := service.Methods().ByName("List")
	getMethod := service.Methods().ByName("Get")
	createMethod := service.Methods().ByName("Create")
	updateMethod := service.Methods().ByName("Update")
	deleteMethod := service.Methods().ByName("Delete")

	objectField := getMethod.Output().Fields().ByName("object")
	if objectField == nil || objectField.Kind() != protoreflect.MessageKind {
		panic("resource Get response has no object field: " + string(service.FullName()))
	}
	objectDescriptor := objectField.Message()
	writable := createMethod != nil && updateMethod != nil && deleteMethod != nil

	return resource{
		key:      strings.ToLower(string(service.Name())),
		title:    resourceTitle(string(service.Name())),
		writable: writable,
		list: func(ctx context.Context, options ListOptions) (ListResult, error) {
			request := dynamicpb.NewMessage(listMethod.Input())
			if err := setIntField(request, "offset", int64(options.Offset)); err != nil {
				return ListResult{}, err
			}
			if err := setIntField(request, "limit", int64(options.Limit)); err != nil {
				return ListResult{}, err
			}
			if err := setStringFieldIfPresent(request, "filter", options.Filter); err != nil {
				return ListResult{}, err
			}
			if err := setStringFieldIfPresent(request, "order", options.Order); err != nil {
				return ListResult{}, err
			}
			response, err := invokeResourceMethod(ctx, conn, listMethod, request)
			if err != nil {
				return ListResult{}, err
			}
			itemsField := response.ProtoReflect().Descriptor().Fields().ByName("items")
			if itemsField == nil || itemsField.Cardinality() != protoreflect.Repeated {
				return ListResult{}, fmt.Errorf("resource List response has no items field: %s", service.FullName())
			}
			items := response.ProtoReflect().Get(itemsField).List()
			rows := make([]Row, 0, items.Len())
			for index := 0; index < items.Len(); index++ {
				rows = append(rows, reflectedRow(items.Get(index).Message().Interface()))
			}
			result := ListResult{Rows: rows, Total: len(rows)}
			if totalField := response.ProtoReflect().Descriptor().Fields().ByName("total"); totalField != nil {
				result.Total = int(response.ProtoReflect().Get(totalField).Int())
			}
			return result, nil
		},
		get: func(ctx context.Context, id string) (proto.Message, error) {
			request := dynamicpb.NewMessage(getMethod.Input())
			if err := setStringField(request, "id", id); err != nil {
				return nil, err
			}
			response, err := invokeResourceMethod(ctx, conn, getMethod, request)
			if err != nil {
				return nil, err
			}
			return responseObject(response)
		},
		create: func(ctx context.Context, object proto.Message) (proto.Message, error) {
			if createMethod == nil {
				return nil, errReadOnly
			}
			request := dynamicpb.NewMessage(createMethod.Input())
			if err := setMessageField(request, "object", object); err != nil {
				return nil, err
			}
			response, err := invokeResourceMethod(ctx, conn, createMethod, request)
			if err != nil {
				return nil, err
			}
			return responseObject(response)
		},
		update: func(ctx context.Context, object proto.Message) (proto.Message, error) {
			if updateMethod == nil {
				return nil, errReadOnly
			}
			request := dynamicpb.NewMessage(updateMethod.Input())
			if err := setMessageField(request, "object", object); err != nil {
				return nil, err
			}
			if err := setBoolField(request, "lock", true); err != nil {
				return nil, err
			}
			response, err := invokeResourceMethod(ctx, conn, updateMethod, request)
			if err != nil {
				return nil, err
			}
			return responseObject(response)
		},
		delete: func(ctx context.Context, id string) error {
			if deleteMethod == nil {
				return errReadOnly
			}
			request := dynamicpb.NewMessage(deleteMethod.Input())
			if err := setStringField(request, "id", id); err != nil {
				return err
			}
			_, err := invokeResourceMethod(ctx, conn, deleteMethod, request)
			return err
		},
		new: func() proto.Message { return dynamicpb.NewMessage(objectDescriptor) },
		row: reflectedRow,
	}
}

func invokeResourceMethod(ctx context.Context, conn *grpc.ClientConn, method protoreflect.MethodDescriptor, request proto.Message) (proto.Message, error) {
	response := dynamicpb.NewMessage(method.Output())
	if err := conn.Invoke(ctx, resourceMethodName(method), request, response); err != nil {
		return nil, err
	}
	return response, nil
}

func resourceMethodName(method protoreflect.MethodDescriptor) string {
	return "/" + string(method.Parent().FullName()) + "/" + string(method.Name())
}

func responseObject(response proto.Message) (proto.Message, error) {
	field := response.ProtoReflect().Descriptor().Fields().ByName("object")
	if field == nil || field.Kind() != protoreflect.MessageKind {
		return nil, errors.New("resource response has no object field")
	}
	return response.ProtoReflect().Get(field).Message().Interface(), nil
}

func setStringField(message *dynamicpb.Message, name, value string) error {
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.StringKind {
		return fmt.Errorf("request has no string field %q", name)
	}
	message.Set(field, protoreflect.ValueOfString(value))
	return nil
}

func setStringFieldIfPresent(message *dynamicpb.Message, name, value string) error {
	if value == "" {
		return nil
	}
	return setStringField(message, name, value)
}

func setIntField(message *dynamicpb.Message, name string, value int64) error {
	if value == 0 {
		return nil
	}
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil {
		return nil
	}
	switch field.Kind() {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		message.Set(field, protoreflect.ValueOfInt32(int32(value)))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		message.Set(field, protoreflect.ValueOfInt64(value))
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		message.Set(field, protoreflect.ValueOfUint32(uint32(value)))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		message.Set(field, protoreflect.ValueOfUint64(uint64(value)))
	default:
		return fmt.Errorf("request field %q is not an integer", name)
	}
	return nil
}

func setBoolField(message *dynamicpb.Message, name string, value bool) error {
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.BoolKind {
		return fmt.Errorf("request has no bool field %q", name)
	}
	message.Set(field, protoreflect.ValueOfBool(value))
	return nil
}

func setMessageField(message *dynamicpb.Message, name string, object proto.Message) error {
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.MessageKind {
		return fmt.Errorf("request has no message field %q", name)
	}
	if object == nil || object.ProtoReflect().Descriptor().FullName() != field.Message().FullName() {
		return fmt.Errorf("request field %q has an incompatible message type", name)
	}
	message.Set(field, protoreflect.ValueOfMessage(object.ProtoReflect()))
	return nil
}

func resourceTitle(name string) string {
	runes := []rune(name)
	words := make([]string, 0, 4)
	start := 0
	for index := 1; index < len(runes); index++ {
		if !unicode.IsUpper(runes[index]) {
			continue
		}
		previousLower := unicode.IsLower(runes[index-1])
		nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		if previousLower || nextLower {
			words = append(words, string(runes[start:index]))
			start = index
		}
	}
	words = append(words, string(runes[start:]))
	return strings.Join(words, " ")
}

func reflectedRow(object proto.Message) Row {
	message := object.ProtoReflect()
	metadata := nestedMessage(message, "metadata")
	status := nestedMessage(message, "status")
	return Row{
		Object:  object,
		ID:      stringField(message, "id"),
		Name:    stringField(metadata, "name"),
		State:   enumField(status, "state"),
		Tenant:  stringField(metadata, "tenant"),
		Created: timestampField(metadata, "creation_timestamp"),
		Version: versionField(metadata),
	}
}

func timestampField(message protoreflect.Message, name string) time.Time {
	if message == nil {
		return time.Time{}
	}
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.MessageKind {
		return time.Time{}
	}
	value := message.Get(field).Message()
	secondsField := value.Descriptor().Fields().ByName("seconds")
	if secondsField == nil {
		return time.Time{}
	}
	seconds := value.Get(secondsField).Int()
	if seconds == 0 {
		return time.Time{}
	}
	nanosField := value.Descriptor().Fields().ByName("nanos")
	nanos := int64(0)
	if nanosField != nil {
		nanos = value.Get(nanosField).Int()
	}
	return time.Unix(seconds, nanos).UTC()
}

func nestedMessage(message protoreflect.Message, name string) protoreflect.Message {
	if message == nil {
		return nil
	}
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.MessageKind || !message.Has(field) {
		return nil
	}
	return message.Get(field).Message()
}

func stringField(message protoreflect.Message, name string) string {
	if message == nil {
		return ""
	}
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.StringKind {
		return ""
	}
	return message.Get(field).String()
}

func enumField(message protoreflect.Message, name string) string {
	if message == nil {
		return ""
	}
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.EnumKind {
		return ""
	}
	value := field.Enum().Values().ByNumber(message.Get(field).Enum())
	if value == nil {
		return strconv.FormatInt(int64(message.Get(field).Enum()), 10)
	}
	return string(value.Name())
}

func versionField(message protoreflect.Message) string {
	if message == nil {
		return ""
	}
	field := message.Descriptor().Fields().ByName("version")
	if field == nil {
		return ""
	}
	switch field.Kind() {
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind:
		return strconv.FormatInt(message.Get(field).Int(), 10)
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		return strconv.FormatUint(message.Get(field).Uint(), 10)
	default:
		return ""
	}
}
