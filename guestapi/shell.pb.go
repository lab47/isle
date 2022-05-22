// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.27.1
// 	protoc        v3.6.1
// source: shell.proto

package guestapi

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Packet_Channel int32

const (
	Packet_STDIN  Packet_Channel = 0
	Packet_STDOUT Packet_Channel = 1
	Packet_STDERR Packet_Channel = 2
)

// Enum value maps for Packet_Channel.
var (
	Packet_Channel_name = map[int32]string{
		0: "STDIN",
		1: "STDOUT",
		2: "STDERR",
	}
	Packet_Channel_value = map[string]int32{
		"STDIN":  0,
		"STDOUT": 1,
		"STDERR": 2,
	}
)

func (x Packet_Channel) Enum() *Packet_Channel {
	p := new(Packet_Channel)
	*p = x
	return p
}

func (x Packet_Channel) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Packet_Channel) Descriptor() protoreflect.EnumDescriptor {
	return file_shell_proto_enumTypes[0].Descriptor()
}

func (Packet_Channel) Type() protoreflect.EnumType {
	return &file_shell_proto_enumTypes[0]
}

func (x Packet_Channel) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Packet_Channel.Descriptor instead.
func (Packet_Channel) EnumDescriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{3, 0}
}

type ShellSession struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name  string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Image string `protobuf:"bytes,2,opt,name=image,proto3" json:"image,omitempty"`
}

func (x *ShellSession) Reset() {
	*x = ShellSession{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ShellSession) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ShellSession) ProtoMessage() {}

func (x *ShellSession) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ShellSession.ProtoReflect.Descriptor instead.
func (*ShellSession) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{0}
}

func (x *ShellSession) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ShellSession) GetImage() string {
	if x != nil {
		return x.Image
	}
	return ""
}

type SessionStart struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Args  []string                 `protobuf:"bytes,1,rep,name=args,proto3" json:"args,omitempty"`
	Env   []*SessionStart_EnvVar   `protobuf:"bytes,2,rep,name=env,proto3" json:"env,omitempty"`
	Image string                   `protobuf:"bytes,3,opt,name=image,proto3" json:"image,omitempty"`
	Name  string                   `protobuf:"bytes,4,opt,name=name,proto3" json:"name,omitempty"`
	Pty   *SessionStart_PTYRequest `protobuf:"bytes,5,opt,name=pty,proto3" json:"pty,omitempty"`
}

func (x *SessionStart) Reset() {
	*x = SessionStart{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SessionStart) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SessionStart) ProtoMessage() {}

func (x *SessionStart) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SessionStart.ProtoReflect.Descriptor instead.
func (*SessionStart) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{1}
}

func (x *SessionStart) GetArgs() []string {
	if x != nil {
		return x.Args
	}
	return nil
}

func (x *SessionStart) GetEnv() []*SessionStart_EnvVar {
	if x != nil {
		return x.Env
	}
	return nil
}

func (x *SessionStart) GetImage() string {
	if x != nil {
		return x.Image
	}
	return ""
}

func (x *SessionStart) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *SessionStart) GetPty() *SessionStart_PTYRequest {
	if x != nil {
		return x.Pty
	}
	return nil
}

type SessionContinue struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Pid   int32  `protobuf:"varint,1,opt,name=pid,proto3" json:"pid,omitempty"`
	Error string `protobuf:"bytes,2,opt,name=error,proto3" json:"error,omitempty"`
}

func (x *SessionContinue) Reset() {
	*x = SessionContinue{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SessionContinue) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SessionContinue) ProtoMessage() {}

func (x *SessionContinue) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SessionContinue.ProtoReflect.Descriptor instead.
func (*SessionContinue) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{2}
}

func (x *SessionContinue) GetPid() int32 {
	if x != nil {
		return x.Pid
	}
	return 0
}

func (x *SessionContinue) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

type Packet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Data         []byte             `protobuf:"bytes,1,opt,name=data,proto3" json:"data,omitempty"`
	Channel      Packet_Channel     `protobuf:"varint,2,opt,name=channel,proto3,enum=dev.lab47.isle.guestapi.Packet_Channel" json:"channel,omitempty"`
	WindowChange *Packet_WindowSize `protobuf:"bytes,3,opt,name=window_change,json=windowChange,proto3" json:"window_change,omitempty"`
	Signal       *Packet_Signal     `protobuf:"bytes,4,opt,name=signal,proto3" json:"signal,omitempty"`
	Exit         *Packet_Exit       `protobuf:"bytes,5,opt,name=exit,proto3" json:"exit,omitempty"`
}

func (x *Packet) Reset() {
	*x = Packet{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Packet) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Packet) ProtoMessage() {}

func (x *Packet) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Packet.ProtoReflect.Descriptor instead.
func (*Packet) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{3}
}

func (x *Packet) GetData() []byte {
	if x != nil {
		return x.Data
	}
	return nil
}

func (x *Packet) GetChannel() Packet_Channel {
	if x != nil {
		return x.Channel
	}
	return Packet_STDIN
}

func (x *Packet) GetWindowChange() *Packet_WindowSize {
	if x != nil {
		return x.WindowChange
	}
	return nil
}

func (x *Packet) GetSignal() *Packet_Signal {
	if x != nil {
		return x.Signal
	}
	return nil
}

func (x *Packet) GetExit() *Packet_Exit {
	if x != nil {
		return x.Exit
	}
	return nil
}

type SessionStart_EnvVar struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Key   string `protobuf:"bytes,1,opt,name=key,proto3" json:"key,omitempty"`
	Value string `protobuf:"bytes,2,opt,name=value,proto3" json:"value,omitempty"`
}

func (x *SessionStart_EnvVar) Reset() {
	*x = SessionStart_EnvVar{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SessionStart_EnvVar) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SessionStart_EnvVar) ProtoMessage() {}

func (x *SessionStart_EnvVar) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SessionStart_EnvVar.ProtoReflect.Descriptor instead.
func (*SessionStart_EnvVar) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{1, 0}
}

func (x *SessionStart_EnvVar) GetKey() string {
	if x != nil {
		return x.Key
	}
	return ""
}

func (x *SessionStart_EnvVar) GetValue() string {
	if x != nil {
		return x.Value
	}
	return ""
}

type SessionStart_PTYRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Term       string             `protobuf:"bytes,1,opt,name=term,proto3" json:"term,omitempty"`
	WindowSize *Packet_WindowSize `protobuf:"bytes,2,opt,name=window_size,json=windowSize,proto3" json:"window_size,omitempty"`
}

func (x *SessionStart_PTYRequest) Reset() {
	*x = SessionStart_PTYRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SessionStart_PTYRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SessionStart_PTYRequest) ProtoMessage() {}

func (x *SessionStart_PTYRequest) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SessionStart_PTYRequest.ProtoReflect.Descriptor instead.
func (*SessionStart_PTYRequest) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{1, 1}
}

func (x *SessionStart_PTYRequest) GetTerm() string {
	if x != nil {
		return x.Term
	}
	return ""
}

func (x *SessionStart_PTYRequest) GetWindowSize() *Packet_WindowSize {
	if x != nil {
		return x.WindowSize
	}
	return nil
}

type Packet_WindowSize struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Width  int32 `protobuf:"varint,1,opt,name=width,proto3" json:"width,omitempty"`
	Height int32 `protobuf:"varint,2,opt,name=height,proto3" json:"height,omitempty"`
}

func (x *Packet_WindowSize) Reset() {
	*x = Packet_WindowSize{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Packet_WindowSize) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Packet_WindowSize) ProtoMessage() {}

func (x *Packet_WindowSize) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Packet_WindowSize.ProtoReflect.Descriptor instead.
func (*Packet_WindowSize) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{3, 0}
}

func (x *Packet_WindowSize) GetWidth() int32 {
	if x != nil {
		return x.Width
	}
	return 0
}

func (x *Packet_WindowSize) GetHeight() int32 {
	if x != nil {
		return x.Height
	}
	return 0
}

type Packet_Signal struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Signal int32 `protobuf:"varint,1,opt,name=signal,proto3" json:"signal,omitempty"`
}

func (x *Packet_Signal) Reset() {
	*x = Packet_Signal{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Packet_Signal) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Packet_Signal) ProtoMessage() {}

func (x *Packet_Signal) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Packet_Signal.ProtoReflect.Descriptor instead.
func (*Packet_Signal) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{3, 1}
}

func (x *Packet_Signal) GetSignal() int32 {
	if x != nil {
		return x.Signal
	}
	return 0
}

type Packet_Exit struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Code int32 `protobuf:"varint,1,opt,name=code,proto3" json:"code,omitempty"`
}

func (x *Packet_Exit) Reset() {
	*x = Packet_Exit{}
	if protoimpl.UnsafeEnabled {
		mi := &file_shell_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Packet_Exit) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Packet_Exit) ProtoMessage() {}

func (x *Packet_Exit) ProtoReflect() protoreflect.Message {
	mi := &file_shell_proto_msgTypes[8]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Packet_Exit.ProtoReflect.Descriptor instead.
func (*Packet_Exit) Descriptor() ([]byte, []int) {
	return file_shell_proto_rawDescGZIP(), []int{3, 2}
}

func (x *Packet_Exit) GetCode() int32 {
	if x != nil {
		return x.Code
	}
	return 0
}

var File_shell_proto protoreflect.FileDescriptor

var file_shell_proto_rawDesc = []byte{
	0x0a, 0x0b, 0x73, 0x68, 0x65, 0x6c, 0x6c, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x17, 0x64,
	0x65, 0x76, 0x2e, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x69, 0x73, 0x6c, 0x65, 0x2e, 0x67, 0x75,
	0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x22, 0x38, 0x0a, 0x0c, 0x53, 0x68, 0x65, 0x6c, 0x6c, 0x53,
	0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x69, 0x6d,
	0x61, 0x67, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x69, 0x6d, 0x61, 0x67, 0x65,
	0x22, 0xf1, 0x02, 0x0a, 0x0c, 0x53, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x53, 0x74, 0x61, 0x72,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x61, 0x72, 0x67, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09, 0x52,
	0x04, 0x61, 0x72, 0x67, 0x73, 0x12, 0x3e, 0x0a, 0x03, 0x65, 0x6e, 0x76, 0x18, 0x02, 0x20, 0x03,
	0x28, 0x0b, 0x32, 0x2c, 0x2e, 0x64, 0x65, 0x76, 0x2e, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x69,
	0x73, 0x6c, 0x65, 0x2e, 0x67, 0x75, 0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x2e, 0x53, 0x65, 0x73,
	0x73, 0x69, 0x6f, 0x6e, 0x53, 0x74, 0x61, 0x72, 0x74, 0x2e, 0x45, 0x6e, 0x76, 0x56, 0x61, 0x72,
	0x52, 0x03, 0x65, 0x6e, 0x76, 0x12, 0x14, 0x0a, 0x05, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12,
	0x42, 0x0a, 0x03, 0x70, 0x74, 0x79, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x30, 0x2e, 0x64,
	0x65, 0x76, 0x2e, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x69, 0x73, 0x6c, 0x65, 0x2e, 0x67, 0x75,
	0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x2e, 0x53, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x53, 0x74,
	0x61, 0x72, 0x74, 0x2e, 0x50, 0x54, 0x59, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x52, 0x03,
	0x70, 0x74, 0x79, 0x1a, 0x30, 0x0a, 0x06, 0x45, 0x6e, 0x76, 0x56, 0x61, 0x72, 0x12, 0x10, 0x0a,
	0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12,
	0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05,
	0x76, 0x61, 0x6c, 0x75, 0x65, 0x1a, 0x6d, 0x0a, 0x0a, 0x50, 0x54, 0x59, 0x52, 0x65, 0x71, 0x75,
	0x65, 0x73, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x74, 0x65, 0x72, 0x6d, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x04, 0x74, 0x65, 0x72, 0x6d, 0x12, 0x4b, 0x0a, 0x0b, 0x77, 0x69, 0x6e, 0x64, 0x6f,
	0x77, 0x5f, 0x73, 0x69, 0x7a, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x2a, 0x2e, 0x64,
	0x65, 0x76, 0x2e, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x69, 0x73, 0x6c, 0x65, 0x2e, 0x67, 0x75,
	0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x2e, 0x50, 0x61, 0x63, 0x6b, 0x65, 0x74, 0x2e, 0x57, 0x69,
	0x6e, 0x64, 0x6f, 0x77, 0x53, 0x69, 0x7a, 0x65, 0x52, 0x0a, 0x77, 0x69, 0x6e, 0x64, 0x6f, 0x77,
	0x53, 0x69, 0x7a, 0x65, 0x22, 0x39, 0x0a, 0x0f, 0x53, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e, 0x43,
	0x6f, 0x6e, 0x74, 0x69, 0x6e, 0x75, 0x65, 0x12, 0x10, 0x0a, 0x03, 0x70, 0x69, 0x64, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x05, 0x52, 0x03, 0x70, 0x69, 0x64, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x72, 0x72,
	0x6f, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x22,
	0xd2, 0x03, 0x0a, 0x06, 0x50, 0x61, 0x63, 0x6b, 0x65, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x64, 0x61,
	0x74, 0x61, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x04, 0x64, 0x61, 0x74, 0x61, 0x12, 0x41,
	0x0a, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0e, 0x32,
	0x27, 0x2e, 0x64, 0x65, 0x76, 0x2e, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x69, 0x73, 0x6c, 0x65,
	0x2e, 0x67, 0x75, 0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x2e, 0x50, 0x61, 0x63, 0x6b, 0x65, 0x74,
	0x2e, 0x43, 0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x52, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x6e, 0x65,
	0x6c, 0x12, 0x4f, 0x0a, 0x0d, 0x77, 0x69, 0x6e, 0x64, 0x6f, 0x77, 0x5f, 0x63, 0x68, 0x61, 0x6e,
	0x67, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x2a, 0x2e, 0x64, 0x65, 0x76, 0x2e, 0x6c,
	0x61, 0x62, 0x34, 0x37, 0x2e, 0x69, 0x73, 0x6c, 0x65, 0x2e, 0x67, 0x75, 0x65, 0x73, 0x74, 0x61,
	0x70, 0x69, 0x2e, 0x50, 0x61, 0x63, 0x6b, 0x65, 0x74, 0x2e, 0x57, 0x69, 0x6e, 0x64, 0x6f, 0x77,
	0x53, 0x69, 0x7a, 0x65, 0x52, 0x0c, 0x77, 0x69, 0x6e, 0x64, 0x6f, 0x77, 0x43, 0x68, 0x61, 0x6e,
	0x67, 0x65, 0x12, 0x3e, 0x0a, 0x06, 0x73, 0x69, 0x67, 0x6e, 0x61, 0x6c, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x26, 0x2e, 0x64, 0x65, 0x76, 0x2e, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x69,
	0x73, 0x6c, 0x65, 0x2e, 0x67, 0x75, 0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x2e, 0x50, 0x61, 0x63,
	0x6b, 0x65, 0x74, 0x2e, 0x53, 0x69, 0x67, 0x6e, 0x61, 0x6c, 0x52, 0x06, 0x73, 0x69, 0x67, 0x6e,
	0x61, 0x6c, 0x12, 0x38, 0x0a, 0x04, 0x65, 0x78, 0x69, 0x74, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x24, 0x2e, 0x64, 0x65, 0x76, 0x2e, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x69, 0x73, 0x6c,
	0x65, 0x2e, 0x67, 0x75, 0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x2e, 0x50, 0x61, 0x63, 0x6b, 0x65,
	0x74, 0x2e, 0x45, 0x78, 0x69, 0x74, 0x52, 0x04, 0x65, 0x78, 0x69, 0x74, 0x1a, 0x3a, 0x0a, 0x0a,
	0x57, 0x69, 0x6e, 0x64, 0x6f, 0x77, 0x53, 0x69, 0x7a, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x77, 0x69,
	0x64, 0x74, 0x68, 0x18, 0x01, 0x20, 0x01, 0x28, 0x05, 0x52, 0x05, 0x77, 0x69, 0x64, 0x74, 0x68,
	0x12, 0x16, 0x0a, 0x06, 0x68, 0x65, 0x69, 0x67, 0x68, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x05,
	0x52, 0x06, 0x68, 0x65, 0x69, 0x67, 0x68, 0x74, 0x1a, 0x20, 0x0a, 0x06, 0x53, 0x69, 0x67, 0x6e,
	0x61, 0x6c, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x69, 0x67, 0x6e, 0x61, 0x6c, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x05, 0x52, 0x06, 0x73, 0x69, 0x67, 0x6e, 0x61, 0x6c, 0x1a, 0x1a, 0x0a, 0x04, 0x45, 0x78,
	0x69, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x63, 0x6f, 0x64, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x05,
	0x52, 0x04, 0x63, 0x6f, 0x64, 0x65, 0x22, 0x2c, 0x0a, 0x07, 0x43, 0x68, 0x61, 0x6e, 0x6e, 0x65,
	0x6c, 0x12, 0x09, 0x0a, 0x05, 0x53, 0x54, 0x44, 0x49, 0x4e, 0x10, 0x00, 0x12, 0x0a, 0x0a, 0x06,
	0x53, 0x54, 0x44, 0x4f, 0x55, 0x54, 0x10, 0x01, 0x12, 0x0a, 0x0a, 0x06, 0x53, 0x54, 0x44, 0x45,
	0x52, 0x52, 0x10, 0x02, 0x42, 0x19, 0x5a, 0x17, 0x6c, 0x61, 0x62, 0x34, 0x37, 0x2e, 0x64, 0x65,
	0x76, 0x2f, 0x69, 0x73, 0x6c, 0x65, 0x2f, 0x67, 0x75, 0x65, 0x73, 0x74, 0x61, 0x70, 0x69, 0x62,
	0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_shell_proto_rawDescOnce sync.Once
	file_shell_proto_rawDescData = file_shell_proto_rawDesc
)

func file_shell_proto_rawDescGZIP() []byte {
	file_shell_proto_rawDescOnce.Do(func() {
		file_shell_proto_rawDescData = protoimpl.X.CompressGZIP(file_shell_proto_rawDescData)
	})
	return file_shell_proto_rawDescData
}

var file_shell_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_shell_proto_msgTypes = make([]protoimpl.MessageInfo, 9)
var file_shell_proto_goTypes = []interface{}{
	(Packet_Channel)(0),             // 0: dev.lab47.isle.guestapi.Packet.Channel
	(*ShellSession)(nil),            // 1: dev.lab47.isle.guestapi.ShellSession
	(*SessionStart)(nil),            // 2: dev.lab47.isle.guestapi.SessionStart
	(*SessionContinue)(nil),         // 3: dev.lab47.isle.guestapi.SessionContinue
	(*Packet)(nil),                  // 4: dev.lab47.isle.guestapi.Packet
	(*SessionStart_EnvVar)(nil),     // 5: dev.lab47.isle.guestapi.SessionStart.EnvVar
	(*SessionStart_PTYRequest)(nil), // 6: dev.lab47.isle.guestapi.SessionStart.PTYRequest
	(*Packet_WindowSize)(nil),       // 7: dev.lab47.isle.guestapi.Packet.WindowSize
	(*Packet_Signal)(nil),           // 8: dev.lab47.isle.guestapi.Packet.Signal
	(*Packet_Exit)(nil),             // 9: dev.lab47.isle.guestapi.Packet.Exit
}
var file_shell_proto_depIdxs = []int32{
	5, // 0: dev.lab47.isle.guestapi.SessionStart.env:type_name -> dev.lab47.isle.guestapi.SessionStart.EnvVar
	6, // 1: dev.lab47.isle.guestapi.SessionStart.pty:type_name -> dev.lab47.isle.guestapi.SessionStart.PTYRequest
	0, // 2: dev.lab47.isle.guestapi.Packet.channel:type_name -> dev.lab47.isle.guestapi.Packet.Channel
	7, // 3: dev.lab47.isle.guestapi.Packet.window_change:type_name -> dev.lab47.isle.guestapi.Packet.WindowSize
	8, // 4: dev.lab47.isle.guestapi.Packet.signal:type_name -> dev.lab47.isle.guestapi.Packet.Signal
	9, // 5: dev.lab47.isle.guestapi.Packet.exit:type_name -> dev.lab47.isle.guestapi.Packet.Exit
	7, // 6: dev.lab47.isle.guestapi.SessionStart.PTYRequest.window_size:type_name -> dev.lab47.isle.guestapi.Packet.WindowSize
	7, // [7:7] is the sub-list for method output_type
	7, // [7:7] is the sub-list for method input_type
	7, // [7:7] is the sub-list for extension type_name
	7, // [7:7] is the sub-list for extension extendee
	0, // [0:7] is the sub-list for field type_name
}

func init() { file_shell_proto_init() }
func file_shell_proto_init() {
	if File_shell_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_shell_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ShellSession); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SessionStart); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SessionContinue); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Packet); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SessionStart_EnvVar); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SessionStart_PTYRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Packet_WindowSize); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Packet_Signal); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_shell_proto_msgTypes[8].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Packet_Exit); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_shell_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   9,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_shell_proto_goTypes,
		DependencyIndexes: file_shell_proto_depIdxs,
		EnumInfos:         file_shell_proto_enumTypes,
		MessageInfos:      file_shell_proto_msgTypes,
	}.Build()
	File_shell_proto = out.File
	file_shell_proto_rawDesc = nil
	file_shell_proto_goTypes = nil
	file_shell_proto_depIdxs = nil
}