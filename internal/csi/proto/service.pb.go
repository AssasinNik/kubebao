// Code generated manually to match secrets-store-csi-driver proto.
// Based on: https://github.com/kubernetes-sigs/secrets-store-csi-driver/blob/main/provider/v1alpha1/service.proto

package proto

import (
	"context"

	"google.golang.org/grpc"
)

// VersionRequest message
type VersionRequest struct {
	Version string `protobuf:"bytes,1,opt,name=version,proto3" json:"version,omitempty"`
}

func (x *VersionRequest) Reset()         { *x = VersionRequest{} }
func (x *VersionRequest) String() string { return "" }
func (*VersionRequest) ProtoMessage()    {}

func (x *VersionRequest) GetVersion() string {
	if x != nil {
		return x.Version
	}
	return ""
}

// VersionResponse message
type VersionResponse struct {
	Version        string `protobuf:"bytes,1,opt,name=version,proto3" json:"version,omitempty"`
	RuntimeName    string `protobuf:"bytes,2,opt,name=runtime_name,json=runtimeName,proto3" json:"runtime_name,omitempty"`
	RuntimeVersion string `protobuf:"bytes,3,opt,name=runtime_version,json=runtimeVersion,proto3" json:"runtime_version,omitempty"`
}

func (x *VersionResponse) Reset()         { *x = VersionResponse{} }
func (x *VersionResponse) String() string { return "" }
func (*VersionResponse) ProtoMessage()    {}

func (x *VersionResponse) GetVersion() string {
	if x != nil {
		return x.Version
	}
	return ""
}

func (x *VersionResponse) GetRuntimeName() string {
	if x != nil {
		return x.RuntimeName
	}
	return ""
}

func (x *VersionResponse) GetRuntimeVersion() string {
	if x != nil {
		return x.RuntimeVersion
	}
	return ""
}

// ObjectVersion message
type ObjectVersion struct {
	Id      string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Version string `protobuf:"bytes,2,opt,name=version,proto3" json:"version,omitempty"`
}

func (x *ObjectVersion) Reset()         { *x = ObjectVersion{} }
func (x *ObjectVersion) String() string { return "" }
func (*ObjectVersion) ProtoMessage()    {}

func (x *ObjectVersion) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *ObjectVersion) GetVersion() string {
	if x != nil {
		return x.Version
	}
	return ""
}

// MountRequest message
type MountRequest struct {
	Attributes           string           `protobuf:"bytes,1,opt,name=attributes,proto3" json:"attributes,omitempty"`
	Secrets              string           `protobuf:"bytes,2,opt,name=secrets,proto3" json:"secrets,omitempty"`
	TargetPath           string           `protobuf:"bytes,3,opt,name=target_path,json=targetPath,proto3" json:"target_path,omitempty"`
	Permission           string           `protobuf:"bytes,4,opt,name=permission,proto3" json:"permission,omitempty"`
	CurrentObjectVersion []*ObjectVersion `protobuf:"bytes,5,rep,name=current_object_version,json=currentObjectVersion,proto3" json:"current_object_version,omitempty"`
}

func (x *MountRequest) Reset()         { *x = MountRequest{} }
func (x *MountRequest) String() string { return "" }
func (*MountRequest) ProtoMessage()    {}

func (x *MountRequest) GetAttributes() string {
	if x != nil {
		return x.Attributes
	}
	return ""
}

func (x *MountRequest) GetSecrets() string {
	if x != nil {
		return x.Secrets
	}
	return ""
}

func (x *MountRequest) GetTargetPath() string {
	if x != nil {
		return x.TargetPath
	}
	return ""
}

func (x *MountRequest) GetPermission() string {
	if x != nil {
		return x.Permission
	}
	return ""
}

func (x *MountRequest) GetCurrentObjectVersion() []*ObjectVersion {
	if x != nil {
		return x.CurrentObjectVersion
	}
	return nil
}

// File message
type File struct {
	Path     string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
	Mode     int32  `protobuf:"varint,2,opt,name=mode,proto3" json:"mode,omitempty"`
	Contents []byte `protobuf:"bytes,3,opt,name=contents,proto3" json:"contents,omitempty"`
}

func (x *File) Reset()         { *x = File{} }
func (x *File) String() string { return "" }
func (*File) ProtoMessage()    {}

func (x *File) GetPath() string {
	if x != nil {
		return x.Path
	}
	return ""
}

func (x *File) GetMode() int32 {
	if x != nil {
		return x.Mode
	}
	return 0
}

func (x *File) GetContents() []byte {
	if x != nil {
		return x.Contents
	}
	return nil
}

// Error message
type Error struct {
	Code string `protobuf:"bytes,1,opt,name=code,proto3" json:"code,omitempty"`
}

func (x *Error) Reset()         { *x = Error{} }
func (x *Error) String() string { return "" }
func (*Error) ProtoMessage()    {}

func (x *Error) GetCode() string {
	if x != nil {
		return x.Code
	}
	return ""
}

// MountResponse message
type MountResponse struct {
	ObjectVersion []*ObjectVersion `protobuf:"bytes,1,rep,name=object_version,json=objectVersion,proto3" json:"object_version,omitempty"`
	Error         *Error           `protobuf:"bytes,2,opt,name=error,proto3" json:"error,omitempty"`
	Files         []*File          `protobuf:"bytes,3,rep,name=files,proto3" json:"files,omitempty"`
}

func (x *MountResponse) Reset()         { *x = MountResponse{} }
func (x *MountResponse) String() string { return "" }
func (*MountResponse) ProtoMessage()    {}

func (x *MountResponse) GetObjectVersion() []*ObjectVersion {
	if x != nil {
		return x.ObjectVersion
	}
	return nil
}

func (x *MountResponse) GetError() *Error {
	if x != nil {
		return x.Error
	}
	return nil
}

func (x *MountResponse) GetFiles() []*File {
	if x != nil {
		return x.Files
	}
	return nil
}

// CSIDriverProviderServer is the server API for CSIDriverProvider service.
type CSIDriverProviderServer interface {
	Version(context.Context, *VersionRequest) (*VersionResponse, error)
	Mount(context.Context, *MountRequest) (*MountResponse, error)
}

// UnimplementedCSIDriverProviderServer can be embedded to have forward compatible implementations.
type UnimplementedCSIDriverProviderServer struct{}

func (UnimplementedCSIDriverProviderServer) Version(context.Context, *VersionRequest) (*VersionResponse, error) {
	return nil, nil
}

func (UnimplementedCSIDriverProviderServer) Mount(context.Context, *MountRequest) (*MountResponse, error) {
	return nil, nil
}

// RegisterCSIDriverProviderServer registers the server with the gRPC server
func RegisterCSIDriverProviderServer(s grpc.ServiceRegistrar, srv CSIDriverProviderServer) {
	s.RegisterService(&CSIDriverProvider_ServiceDesc, srv)
}

// CSIDriverProvider_ServiceDesc is the grpc.ServiceDesc for CSIDriverProvider service.
var CSIDriverProvider_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "v1alpha1.CSIDriverProvider",
	HandlerType: (*CSIDriverProviderServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Version",
			Handler:    _CSIDriverProvider_Version_Handler,
		},
		{
			MethodName: "Mount",
			Handler:    _CSIDriverProvider_Mount_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "service.proto",
}

func _CSIDriverProvider_Version_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(VersionRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CSIDriverProviderServer).Version(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/v1alpha1.CSIDriverProvider/Version",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CSIDriverProviderServer).Version(ctx, req.(*VersionRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CSIDriverProvider_Mount_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MountRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CSIDriverProviderServer).Mount(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/v1alpha1.CSIDriverProvider/Mount",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CSIDriverProviderServer).Mount(ctx, req.(*MountRequest))
	}
	return interceptor(ctx, in, info, handler)
}
