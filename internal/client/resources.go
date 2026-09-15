package client

import (
	"context"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type resource struct {
	key    string
	title  string
	list   func(context.Context) ([]Row, error)
	get    func(context.Context, string) (proto.Message, error)
	create func(context.Context, proto.Message) (proto.Message, error)
	update func(context.Context, proto.Message) (proto.Message, error)
	delete func(context.Context, string) error
	new    func() proto.Message
	row    func(proto.Message) Row
}

func (r resource) Key() string { return r.key }

func (r resource) Title() string { return r.title }

func (r resource) List(ctx context.Context) ([]Row, error) { return r.list(ctx) }

func (r resource) Get(ctx context.Context, id string) (proto.Message, error) { return r.get(ctx, id) }

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
	return map[string]Resource{
		"clusters":         clusterResource(publicv1.NewClustersClient(conn)),
		"computeinstances": computeInstanceResource(publicv1.NewComputeInstancesClient(conn)),
		"virtualnetworks":  virtualNetworkResource(publicv1.NewVirtualNetworksClient(conn)),
		"subnets":          subnetResource(publicv1.NewSubnetsClient(conn)),
		"securitygroups":   securityGroupResource(publicv1.NewSecurityGroupsClient(conn)),
	}
}

func clusterResource(service publicv1.ClustersClient) Resource {
	return resource{
		key:   "clusters",
		title: "Clusters",
		list: func(ctx context.Context) ([]Row, error) {
			response, err := service.List(ctx, &publicv1.ClustersListRequest{})
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(response.GetItems()))
			for _, object := range response.GetItems() {
				rows = append(rows, clusterRow(object))
			}
			return rows, nil
		},
		get: func(ctx context.Context, id string) (proto.Message, error) {
			response, err := service.Get(ctx, &publicv1.ClustersGetRequest{Id: id})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		create: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Create(ctx, &publicv1.ClustersCreateRequest{Object: message.(*publicv1.Cluster)})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		update: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Update(ctx, &publicv1.ClustersUpdateRequest{Object: message.(*publicv1.Cluster), Lock: true})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		delete: func(ctx context.Context, id string) error {
			_, err := service.Delete(ctx, &publicv1.ClustersDeleteRequest{Id: id})
			return err
		},
		new: func() proto.Message { return &publicv1.Cluster{} },
		row: func(object proto.Message) Row { return clusterRow(object.(*publicv1.Cluster)) },
	}
}

func clusterRow(object *publicv1.Cluster) Row {
	return metadataRow(object, object.GetMetadata(), object.GetId(), object.GetStatus().GetState().String())
}

func computeInstanceResource(service publicv1.ComputeInstancesClient) Resource {
	return resource{
		key:   "computeinstances",
		title: "Compute Instances",
		list: func(ctx context.Context) ([]Row, error) {
			response, err := service.List(ctx, &publicv1.ComputeInstancesListRequest{})
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(response.GetItems()))
			for _, object := range response.GetItems() {
				rows = append(rows, computeInstanceRow(object))
			}
			return rows, nil
		},
		get: func(ctx context.Context, id string) (proto.Message, error) {
			response, err := service.Get(ctx, &publicv1.ComputeInstancesGetRequest{Id: id})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		create: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Create(ctx, &publicv1.ComputeInstancesCreateRequest{Object: message.(*publicv1.ComputeInstance)})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		update: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Update(ctx, &publicv1.ComputeInstancesUpdateRequest{Object: message.(*publicv1.ComputeInstance), Lock: true})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		delete: func(ctx context.Context, id string) error {
			_, err := service.Delete(ctx, &publicv1.ComputeInstancesDeleteRequest{Id: id})
			return err
		},
		new: func() proto.Message { return &publicv1.ComputeInstance{} },
		row: func(object proto.Message) Row { return computeInstanceRow(object.(*publicv1.ComputeInstance)) },
	}
}

func computeInstanceRow(object *publicv1.ComputeInstance) Row {
	return metadataRow(object, object.GetMetadata(), object.GetId(), object.GetStatus().GetState().String())
}

func virtualNetworkResource(service publicv1.VirtualNetworksClient) Resource {
	return resource{
		key:   "virtualnetworks",
		title: "Virtual Networks",
		list: func(ctx context.Context) ([]Row, error) {
			response, err := service.List(ctx, &publicv1.VirtualNetworksListRequest{})
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(response.GetItems()))
			for _, object := range response.GetItems() {
				rows = append(rows, virtualNetworkRow(object))
			}
			return rows, nil
		},
		get: func(ctx context.Context, id string) (proto.Message, error) {
			response, err := service.Get(ctx, &publicv1.VirtualNetworksGetRequest{Id: id})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		create: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Create(ctx, &publicv1.VirtualNetworksCreateRequest{Object: message.(*publicv1.VirtualNetwork)})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		update: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Update(ctx, &publicv1.VirtualNetworksUpdateRequest{Object: message.(*publicv1.VirtualNetwork), Lock: true})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		delete: func(ctx context.Context, id string) error {
			_, err := service.Delete(ctx, &publicv1.VirtualNetworksDeleteRequest{Id: id})
			return err
		},
		new: func() proto.Message { return &publicv1.VirtualNetwork{} },
		row: func(object proto.Message) Row { return virtualNetworkRow(object.(*publicv1.VirtualNetwork)) },
	}
}

func virtualNetworkRow(object *publicv1.VirtualNetwork) Row {
	return metadataRow(object, object.GetMetadata(), object.GetId(), object.GetStatus().GetState().String())
}

func subnetResource(service publicv1.SubnetsClient) Resource {
	return resource{
		key:   "subnets",
		title: "Subnets",
		list: func(ctx context.Context) ([]Row, error) {
			response, err := service.List(ctx, &publicv1.SubnetsListRequest{})
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(response.GetItems()))
			for _, object := range response.GetItems() {
				rows = append(rows, subnetRow(object))
			}
			return rows, nil
		},
		get: func(ctx context.Context, id string) (proto.Message, error) {
			response, err := service.Get(ctx, &publicv1.SubnetsGetRequest{Id: id})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		create: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Create(ctx, &publicv1.SubnetsCreateRequest{Object: message.(*publicv1.Subnet)})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		update: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Update(ctx, &publicv1.SubnetsUpdateRequest{Object: message.(*publicv1.Subnet), Lock: true})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		delete: func(ctx context.Context, id string) error {
			_, err := service.Delete(ctx, &publicv1.SubnetsDeleteRequest{Id: id})
			return err
		},
		new: func() proto.Message { return &publicv1.Subnet{} },
		row: func(object proto.Message) Row { return subnetRow(object.(*publicv1.Subnet)) },
	}
}

func subnetRow(object *publicv1.Subnet) Row {
	return metadataRow(object, object.GetMetadata(), object.GetId(), object.GetStatus().GetState().String())
}

func securityGroupResource(service publicv1.SecurityGroupsClient) Resource {
	return resource{
		key:   "securitygroups",
		title: "Security Groups",
		list: func(ctx context.Context) ([]Row, error) {
			response, err := service.List(ctx, &publicv1.SecurityGroupsListRequest{})
			if err != nil {
				return nil, err
			}
			rows := make([]Row, 0, len(response.GetItems()))
			for _, object := range response.GetItems() {
				rows = append(rows, securityGroupRow(object))
			}
			return rows, nil
		},
		get: func(ctx context.Context, id string) (proto.Message, error) {
			response, err := service.Get(ctx, &publicv1.SecurityGroupsGetRequest{Id: id})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		create: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Create(ctx, &publicv1.SecurityGroupsCreateRequest{Object: message.(*publicv1.SecurityGroup)})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		update: func(ctx context.Context, message proto.Message) (proto.Message, error) {
			response, err := service.Update(ctx, &publicv1.SecurityGroupsUpdateRequest{Object: message.(*publicv1.SecurityGroup), Lock: true})
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		},
		delete: func(ctx context.Context, id string) error {
			_, err := service.Delete(ctx, &publicv1.SecurityGroupsDeleteRequest{Id: id})
			return err
		},
		new: func() proto.Message { return &publicv1.SecurityGroup{} },
		row: func(object proto.Message) Row { return securityGroupRow(object.(*publicv1.SecurityGroup)) },
	}
}

func securityGroupRow(object *publicv1.SecurityGroup) Row {
	return metadataRow(object, object.GetMetadata(), object.GetId(), object.GetStatus().GetState().String())
}
