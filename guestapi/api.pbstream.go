// Code generated by protoc-gen-isle-rpc. DO NOT EDIT.
//
// Source: api.proto

package guestapi

import (
	context "context"
	errors "errors"
	pbstream "github.com/lab47/isle/pkg/pbstream"
)

// This is a compile-time assertion to ensure that this generated file and the connect package are
// compatible. If you get a compiler error that this constant is not defined, this code was
// generated with a version of connect newer than the one compiled into your binary. You can fix the
// problem by either regenerating this code with an older version of connect or updating the connect
// version compiled into your binary.
const _ = pbstream.IsAtLeastVersion0_1_0

const (
	// GuestAPIName is the fully-qualified name of the GuestAPI service.
	GuestAPIName = "dev.lab47.isle.guestapi.GuestAPI"
	// HostAPIName is the fully-qualified name of the HostAPI service.
	HostAPIName = "dev.lab47.isle.guestapi.HostAPI"
	// VMAPIName is the fully-qualified name of the VMAPI service.
	VMAPIName = "dev.lab47.isle.guestapi.VMAPI"
	// StartupAPIName is the fully-qualified name of the StartupAPI service.
	StartupAPIName = "dev.lab47.isle.guestapi.StartupAPI"
)

// PBSGuestAPIClient is a client for the dev.lab47.isle.guestapi.GuestAPI service.
type PBSGuestAPIClient interface {
	AddApp(context.Context, *pbstream.Request[AddAppReq]) (*pbstream.Response[AddAppResp], error)
	DisableApp(context.Context, *pbstream.Request[DisableAppReq]) (*pbstream.Response[DisableAppResp], error)
	RunOnMac(context.Context) *pbstream.BidiStreamForClient[RunInput, RunOutput]
	TrimMemory(context.Context, *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error)
}

// PBSNewGuestAPIClient constructs a client for the dev.lab47.isle.guestapi.GuestAPI service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func PBSNewGuestAPIClient(client pbstream.Client, opts ...pbstream.ClientOption) PBSGuestAPIClient {
	return &pBSGuestAPIClient{
		addApp: pbstream.NewEndpoint[AddAppReq, AddAppResp](
			client,
			"/dev.lab47.isle.guestapi.GuestAPI/AddApp",
		),
		disableApp: pbstream.NewEndpoint[DisableAppReq, DisableAppResp](
			client,
			"/dev.lab47.isle.guestapi.GuestAPI/DisableApp",
		),
		runOnMac: pbstream.NewEndpoint[RunInput, RunOutput](
			client,
			"/dev.lab47.isle.guestapi.GuestAPI/RunOnMac",
		),
		trimMemory: pbstream.NewEndpoint[TrimMemoryReq, TrimMemoryResp](
			client,
			"/dev.lab47.isle.guestapi.GuestAPI/TrimMemory",
		),
	}
}

// pBSGuestAPIClient implements PBSGuestAPIClient.
type pBSGuestAPIClient struct {
	addApp     *pbstream.Endpoint[AddAppReq, AddAppResp]
	disableApp *pbstream.Endpoint[DisableAppReq, DisableAppResp]
	runOnMac   *pbstream.Endpoint[RunInput, RunOutput]
	trimMemory *pbstream.Endpoint[TrimMemoryReq, TrimMemoryResp]
}

// AddApp calls dev.lab47.isle.guestapi.GuestAPI.AddApp.
func (c *pBSGuestAPIClient) AddApp(ctx context.Context, req *pbstream.Request[AddAppReq]) (*pbstream.Response[AddAppResp], error) {
	return c.addApp.CallUnary(ctx, req)
}

// DisableApp calls dev.lab47.isle.guestapi.GuestAPI.DisableApp.
func (c *pBSGuestAPIClient) DisableApp(ctx context.Context, req *pbstream.Request[DisableAppReq]) (*pbstream.Response[DisableAppResp], error) {
	return c.disableApp.CallUnary(ctx, req)
}

// RunOnMac calls dev.lab47.isle.guestapi.GuestAPI.RunOnMac.
func (c *pBSGuestAPIClient) RunOnMac(ctx context.Context) *pbstream.BidiStreamForClient[RunInput, RunOutput] {
	return c.runOnMac.CallBidiStream(ctx)
}

// TrimMemory calls dev.lab47.isle.guestapi.GuestAPI.TrimMemory.
func (c *pBSGuestAPIClient) TrimMemory(ctx context.Context, req *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error) {
	return c.trimMemory.CallUnary(ctx, req)
}

// PBSGuestAPIHandler is an implementation of the dev.lab47.isle.guestapi.GuestAPI service.
type PBSGuestAPIHandler interface {
	AddApp(context.Context, *pbstream.Request[AddAppReq]) (*pbstream.Response[AddAppResp], error)
	DisableApp(context.Context, *pbstream.Request[DisableAppReq]) (*pbstream.Response[DisableAppResp], error)
	RunOnMac(context.Context, *pbstream.BidiStream[RunInput, RunOutput]) error
	TrimMemory(context.Context, *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error)
}

// PBSNewGuestAPIHandler builds an HTTP handler from the service implementation. It returns the path
// on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func PBSNewGuestAPIHandler(svc PBSGuestAPIHandler, opts ...pbstream.HandlerOption) (string, pbstream.Handler) {
	mux := pbstream.NewMux()
	mux.Handle("/dev.lab47.isle.guestapi.GuestAPI/AddApp", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.GuestAPI/AddApp",
		svc.AddApp,
	))
	mux.Handle("/dev.lab47.isle.guestapi.GuestAPI/DisableApp", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.GuestAPI/DisableApp",
		svc.DisableApp,
	))
	mux.Handle("/dev.lab47.isle.guestapi.GuestAPI/RunOnMac", pbstream.NewBidiStreamHandler(
		"/dev.lab47.isle.guestapi.GuestAPI/RunOnMac",
		svc.RunOnMac,
	))
	mux.Handle("/dev.lab47.isle.guestapi.GuestAPI/TrimMemory", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.GuestAPI/TrimMemory",
		svc.TrimMemory,
	))
	return "/dev.lab47.isle.guestapi.GuestAPI/", mux
}

// PBSUnimplementedGuestAPIHandler returns CodeUnimplemented from all methods.
type PBSUnimplementedGuestAPIHandler struct{}

func (PBSUnimplementedGuestAPIHandler) AddApp(context.Context, *pbstream.Request[AddAppReq]) (*pbstream.Response[AddAppResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.GuestAPI.AddApp is not implemented"))
}

func (PBSUnimplementedGuestAPIHandler) DisableApp(context.Context, *pbstream.Request[DisableAppReq]) (*pbstream.Response[DisableAppResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.GuestAPI.DisableApp is not implemented"))
}

func (PBSUnimplementedGuestAPIHandler) RunOnMac(context.Context, *pbstream.BidiStream[RunInput, RunOutput]) error {
	return pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.GuestAPI.RunOnMac is not implemented"))
}

func (PBSUnimplementedGuestAPIHandler) TrimMemory(context.Context, *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.GuestAPI.TrimMemory is not implemented"))
}

// PBSHostAPIClient is a client for the dev.lab47.isle.guestapi.HostAPI service.
type PBSHostAPIClient interface {
	RunOnMac(context.Context) *pbstream.BidiStreamForClient[RunInput, RunOutput]
	Running(context.Context, *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error)
	StartPortForward(context.Context, *pbstream.Request[StartPortForwardReq]) (*pbstream.Response[StartPortForwardResp], error)
	CancelPortForward(context.Context, *pbstream.Request[CancelPortForwardReq]) (*pbstream.Response[CancelPortForwardResp], error)
	TrimMemory(context.Context, *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error)
}

// PBSNewHostAPIClient constructs a client for the dev.lab47.isle.guestapi.HostAPI service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func PBSNewHostAPIClient(client pbstream.Client, opts ...pbstream.ClientOption) PBSHostAPIClient {
	return &pBSHostAPIClient{
		runOnMac: pbstream.NewEndpoint[RunInput, RunOutput](
			client,
			"/dev.lab47.isle.guestapi.HostAPI/RunOnMac",
		),
		running: pbstream.NewEndpoint[RunningReq, RunningResp](
			client,
			"/dev.lab47.isle.guestapi.HostAPI/Running",
		),
		startPortForward: pbstream.NewEndpoint[StartPortForwardReq, StartPortForwardResp](
			client,
			"/dev.lab47.isle.guestapi.HostAPI/StartPortForward",
		),
		cancelPortForward: pbstream.NewEndpoint[CancelPortForwardReq, CancelPortForwardResp](
			client,
			"/dev.lab47.isle.guestapi.HostAPI/CancelPortForward",
		),
		trimMemory: pbstream.NewEndpoint[TrimMemoryReq, TrimMemoryResp](
			client,
			"/dev.lab47.isle.guestapi.HostAPI/TrimMemory",
		),
	}
}

// pBSHostAPIClient implements PBSHostAPIClient.
type pBSHostAPIClient struct {
	runOnMac          *pbstream.Endpoint[RunInput, RunOutput]
	running           *pbstream.Endpoint[RunningReq, RunningResp]
	startPortForward  *pbstream.Endpoint[StartPortForwardReq, StartPortForwardResp]
	cancelPortForward *pbstream.Endpoint[CancelPortForwardReq, CancelPortForwardResp]
	trimMemory        *pbstream.Endpoint[TrimMemoryReq, TrimMemoryResp]
}

// RunOnMac calls dev.lab47.isle.guestapi.HostAPI.RunOnMac.
func (c *pBSHostAPIClient) RunOnMac(ctx context.Context) *pbstream.BidiStreamForClient[RunInput, RunOutput] {
	return c.runOnMac.CallBidiStream(ctx)
}

// Running calls dev.lab47.isle.guestapi.HostAPI.Running.
func (c *pBSHostAPIClient) Running(ctx context.Context, req *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error) {
	return c.running.CallUnary(ctx, req)
}

// StartPortForward calls dev.lab47.isle.guestapi.HostAPI.StartPortForward.
func (c *pBSHostAPIClient) StartPortForward(ctx context.Context, req *pbstream.Request[StartPortForwardReq]) (*pbstream.Response[StartPortForwardResp], error) {
	return c.startPortForward.CallUnary(ctx, req)
}

// CancelPortForward calls dev.lab47.isle.guestapi.HostAPI.CancelPortForward.
func (c *pBSHostAPIClient) CancelPortForward(ctx context.Context, req *pbstream.Request[CancelPortForwardReq]) (*pbstream.Response[CancelPortForwardResp], error) {
	return c.cancelPortForward.CallUnary(ctx, req)
}

// TrimMemory calls dev.lab47.isle.guestapi.HostAPI.TrimMemory.
func (c *pBSHostAPIClient) TrimMemory(ctx context.Context, req *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error) {
	return c.trimMemory.CallUnary(ctx, req)
}

// PBSHostAPIHandler is an implementation of the dev.lab47.isle.guestapi.HostAPI service.
type PBSHostAPIHandler interface {
	RunOnMac(context.Context, *pbstream.BidiStream[RunInput, RunOutput]) error
	Running(context.Context, *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error)
	StartPortForward(context.Context, *pbstream.Request[StartPortForwardReq]) (*pbstream.Response[StartPortForwardResp], error)
	CancelPortForward(context.Context, *pbstream.Request[CancelPortForwardReq]) (*pbstream.Response[CancelPortForwardResp], error)
	TrimMemory(context.Context, *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error)
}

// PBSNewHostAPIHandler builds an HTTP handler from the service implementation. It returns the path
// on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func PBSNewHostAPIHandler(svc PBSHostAPIHandler, opts ...pbstream.HandlerOption) (string, pbstream.Handler) {
	mux := pbstream.NewMux()
	mux.Handle("/dev.lab47.isle.guestapi.HostAPI/RunOnMac", pbstream.NewBidiStreamHandler(
		"/dev.lab47.isle.guestapi.HostAPI/RunOnMac",
		svc.RunOnMac,
	))
	mux.Handle("/dev.lab47.isle.guestapi.HostAPI/Running", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.HostAPI/Running",
		svc.Running,
	))
	mux.Handle("/dev.lab47.isle.guestapi.HostAPI/StartPortForward", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.HostAPI/StartPortForward",
		svc.StartPortForward,
	))
	mux.Handle("/dev.lab47.isle.guestapi.HostAPI/CancelPortForward", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.HostAPI/CancelPortForward",
		svc.CancelPortForward,
	))
	mux.Handle("/dev.lab47.isle.guestapi.HostAPI/TrimMemory", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.HostAPI/TrimMemory",
		svc.TrimMemory,
	))
	return "/dev.lab47.isle.guestapi.HostAPI/", mux
}

// PBSUnimplementedHostAPIHandler returns CodeUnimplemented from all methods.
type PBSUnimplementedHostAPIHandler struct{}

func (PBSUnimplementedHostAPIHandler) RunOnMac(context.Context, *pbstream.BidiStream[RunInput, RunOutput]) error {
	return pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.HostAPI.RunOnMac is not implemented"))
}

func (PBSUnimplementedHostAPIHandler) Running(context.Context, *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.HostAPI.Running is not implemented"))
}

func (PBSUnimplementedHostAPIHandler) StartPortForward(context.Context, *pbstream.Request[StartPortForwardReq]) (*pbstream.Response[StartPortForwardResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.HostAPI.StartPortForward is not implemented"))
}

func (PBSUnimplementedHostAPIHandler) CancelPortForward(context.Context, *pbstream.Request[CancelPortForwardReq]) (*pbstream.Response[CancelPortForwardResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.HostAPI.CancelPortForward is not implemented"))
}

func (PBSUnimplementedHostAPIHandler) TrimMemory(context.Context, *pbstream.Request[TrimMemoryReq]) (*pbstream.Response[TrimMemoryResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.HostAPI.TrimMemory is not implemented"))
}

// PBSVMAPIClient is a client for the dev.lab47.isle.guestapi.VMAPI service.
type PBSVMAPIClient interface {
	RequestShutdown(context.Context, *pbstream.Request[Empty]) (*pbstream.Response[Empty], error)
}

// PBSNewVMAPIClient constructs a client for the dev.lab47.isle.guestapi.VMAPI service. By default,
// it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses, and
// sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the connect.WithGRPC()
// or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func PBSNewVMAPIClient(client pbstream.Client, opts ...pbstream.ClientOption) PBSVMAPIClient {
	return &pBSVMAPIClient{
		requestShutdown: pbstream.NewEndpoint[Empty, Empty](
			client,
			"/dev.lab47.isle.guestapi.VMAPI/RequestShutdown",
		),
	}
}

// pBSVMAPIClient implements PBSVMAPIClient.
type pBSVMAPIClient struct {
	requestShutdown *pbstream.Endpoint[Empty, Empty]
}

// RequestShutdown calls dev.lab47.isle.guestapi.VMAPI.RequestShutdown.
func (c *pBSVMAPIClient) RequestShutdown(ctx context.Context, req *pbstream.Request[Empty]) (*pbstream.Response[Empty], error) {
	return c.requestShutdown.CallUnary(ctx, req)
}

// PBSVMAPIHandler is an implementation of the dev.lab47.isle.guestapi.VMAPI service.
type PBSVMAPIHandler interface {
	RequestShutdown(context.Context, *pbstream.Request[Empty]) (*pbstream.Response[Empty], error)
}

// PBSNewVMAPIHandler builds an HTTP handler from the service implementation. It returns the path on
// which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func PBSNewVMAPIHandler(svc PBSVMAPIHandler, opts ...pbstream.HandlerOption) (string, pbstream.Handler) {
	mux := pbstream.NewMux()
	mux.Handle("/dev.lab47.isle.guestapi.VMAPI/RequestShutdown", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.VMAPI/RequestShutdown",
		svc.RequestShutdown,
	))
	return "/dev.lab47.isle.guestapi.VMAPI/", mux
}

// PBSUnimplementedVMAPIHandler returns CodeUnimplemented from all methods.
type PBSUnimplementedVMAPIHandler struct{}

func (PBSUnimplementedVMAPIHandler) RequestShutdown(context.Context, *pbstream.Request[Empty]) (*pbstream.Response[Empty], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.VMAPI.RequestShutdown is not implemented"))
}

// PBSStartupAPIClient is a client for the dev.lab47.isle.guestapi.StartupAPI service.
type PBSStartupAPIClient interface {
	VMRunning(context.Context, *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error)
}

// PBSNewStartupAPIClient constructs a client for the dev.lab47.isle.guestapi.StartupAPI service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func PBSNewStartupAPIClient(client pbstream.Client, opts ...pbstream.ClientOption) PBSStartupAPIClient {
	return &pBSStartupAPIClient{
		vMRunning: pbstream.NewEndpoint[RunningReq, RunningResp](
			client,
			"/dev.lab47.isle.guestapi.StartupAPI/VMRunning",
		),
	}
}

// pBSStartupAPIClient implements PBSStartupAPIClient.
type pBSStartupAPIClient struct {
	vMRunning *pbstream.Endpoint[RunningReq, RunningResp]
}

// VMRunning calls dev.lab47.isle.guestapi.StartupAPI.VMRunning.
func (c *pBSStartupAPIClient) VMRunning(ctx context.Context, req *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error) {
	return c.vMRunning.CallUnary(ctx, req)
}

// PBSStartupAPIHandler is an implementation of the dev.lab47.isle.guestapi.StartupAPI service.
type PBSStartupAPIHandler interface {
	VMRunning(context.Context, *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error)
}

// PBSNewStartupAPIHandler builds an HTTP handler from the service implementation. It returns the
// path on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func PBSNewStartupAPIHandler(svc PBSStartupAPIHandler, opts ...pbstream.HandlerOption) (string, pbstream.Handler) {
	mux := pbstream.NewMux()
	mux.Handle("/dev.lab47.isle.guestapi.StartupAPI/VMRunning", pbstream.NewUnaryHandler(
		"/dev.lab47.isle.guestapi.StartupAPI/VMRunning",
		svc.VMRunning,
	))
	return "/dev.lab47.isle.guestapi.StartupAPI/", mux
}

// PBSUnimplementedStartupAPIHandler returns CodeUnimplemented from all methods.
type PBSUnimplementedStartupAPIHandler struct{}

func (PBSUnimplementedStartupAPIHandler) VMRunning(context.Context, *pbstream.Request[RunningReq]) (*pbstream.Response[RunningResp], error) {
	return nil, pbstream.NewError(pbstream.CodeUnimplemented, errors.New("dev.lab47.isle.guestapi.StartupAPI.VMRunning is not implemented"))
}
