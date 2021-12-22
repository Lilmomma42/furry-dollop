/*
 *
 * Copyright 2020 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"

	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	contextPackage = protogen.GoImportPath("context")
	grpcPackage    = protogen.GoImportPath("google.golang.org/grpc")
	codesPackage   = protogen.GoImportPath("google.golang.org/grpc/codes")
	statusPackage  = protogen.GoImportPath("google.golang.org/grpc/status")
)

// generateFile generates a _grpc.pb.go file containing gRPC service definitions.
func generateFile(gen *protogen.Plugin, file *protogen.File) *protogen.GeneratedFile {
	if len(file.Services) == 0 {
		return nil
	}
	filename := file.GeneratedFilenamePrefix + "_grpc.pb.go"
	g := gen.NewGeneratedFile(filename, file.GoImportPath)
	g.P("// Code generated by protoc-gen-go-grpc. DO NOT EDIT.")
	g.P()
	g.P("package ", file.GoPackageName)
	g.P()
	generateFileContent(gen, file, g)
	return g
}

// generateFileContent generates the gRPC service definitions, excluding the package statement.
func generateFileContent(gen *protogen.Plugin, file *protogen.File, g *protogen.GeneratedFile) {
	if len(file.Services) == 0 {
		return
	}

	g.P("// This is a compile-time assertion to ensure that this generated file")
	g.P("// is compatible with the grpc package it is being compiled against.")
	g.P("const _ = ", grpcPackage.Ident("SupportPackageIsVersion7"))
	g.P()
	for _, service := range file.Services {
		genClient(gen, file, g, service)
		genService(gen, file, g, service)
		genUnstableServiceInterface(gen, file, g, service)
	}
}

func genClient(gen *protogen.Plugin, file *protogen.File, g *protogen.GeneratedFile, service *protogen.Service) {
	if *migrationMode {
		return
	}
	clientName := service.GoName + "Client"

	g.P("// ", clientName, " is the client API for ", service.GoName, " service.")
	g.P("//")
	g.P("// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.")

	// Client interface.
	if service.Desc.Options().(*descriptorpb.ServiceOptions).GetDeprecated() {
		g.P("//")
		g.P(deprecationComment)
	}
	g.Annotate(clientName, service.Location)
	g.P("type ", clientName, " interface {")
	for _, method := range service.Methods {
		g.Annotate(clientName+"."+method.GoName, method.Location)
		if method.Desc.Options().(*descriptorpb.MethodOptions).GetDeprecated() {
			g.P(deprecationComment)
		}
		g.P(method.Comments.Leading,
			clientSignature(g, method))
	}
	g.P("}")
	g.P()

	// Client structure.
	g.P("type ", unexport(clientName), " struct {")
	g.P("cc ", grpcPackage.Ident("ClientConnInterface"))
	g.P("}")
	g.P()

	// NewClient factory.
	if service.Desc.Options().(*descriptorpb.ServiceOptions).GetDeprecated() {
		g.P(deprecationComment)
	}
	g.P("func New", clientName, " (cc ", grpcPackage.Ident("ClientConnInterface"), ") ", clientName, " {")
	g.P("return &", unexport(clientName), "{cc}")
	g.P("}")
	g.P()

	// Client method implementations.
	for _, method := range service.Methods {
		genClientMethod(gen, g, method)
	}
}

func clientSignature(g *protogen.GeneratedFile, method *protogen.Method) string {
	s := method.GoName + "(ctx " + g.QualifiedGoIdent(contextPackage.Ident("Context"))
	if !method.Desc.IsStreamingClient() {
		s += ", in *" + g.QualifiedGoIdent(method.Input.GoIdent)
	}
	s += ", opts ..." + g.QualifiedGoIdent(grpcPackage.Ident("CallOption")) + ") ("
	if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
		s += "*" + g.QualifiedGoIdent(method.Output.GoIdent)
	} else {
		s += method.Parent.GoName + "_" + method.GoName + "Client"
	}
	s += ", error)"
	return s
}

func genClientMethod(gen *protogen.Plugin, g *protogen.GeneratedFile, method *protogen.Method) {
	service := method.Parent
	sname := fmt.Sprintf("/%s/%s", service.Desc.FullName(), method.Desc.Name())

	if method.Desc.Options().(*descriptorpb.MethodOptions).GetDeprecated() {
		g.P(deprecationComment)
	}

	streamDescName := unexport(service.GoName) + method.GoName + "StreamDesc"
	g.P("var ", streamDescName, " = &", grpcPackage.Ident("StreamDesc"), "{")
	g.P("StreamName: ", strconv.Quote(string(method.Desc.Name())), ",")
	if method.Desc.IsStreamingServer() {
		g.P("ServerStreams: true,")
	}
	if method.Desc.IsStreamingClient() {
		g.P("ClientStreams: true,")
	}
	g.P("}")

	g.P("func (c *", unexport(service.GoName), "Client) ", clientSignature(g, method), "{")
	if !method.Desc.IsStreamingServer() && !method.Desc.IsStreamingClient() {
		g.P("out := new(", method.Output.GoIdent, ")")
		g.P(`err := c.cc.Invoke(ctx, "`, sname, `", in, out, opts...)`)
		g.P("if err != nil { return nil, err }")
		g.P("return out, nil")
		g.P("}")
		g.P()
		return
	}
	streamType := unexport(service.GoName) + method.GoName + "Client"

	g.P(`stream, err := c.cc.NewStream(ctx, `, streamDescName, `, "`, sname, `", opts...)`)
	g.P("if err != nil { return nil, err }")
	g.P("x := &", streamType, "{stream}")
	if !method.Desc.IsStreamingClient() {
		g.P("if err := x.ClientStream.SendMsg(in); err != nil { return nil, err }")
		g.P("if err := x.ClientStream.CloseSend(); err != nil { return nil, err }")
	}
	g.P("return x, nil")
	g.P("}")
	g.P()

	genSend := method.Desc.IsStreamingClient()
	genRecv := method.Desc.IsStreamingServer()
	genCloseAndRecv := !method.Desc.IsStreamingServer()

	// Stream auxiliary types and methods.
	g.P("type ", service.GoName, "_", method.GoName, "Client interface {")
	if genSend {
		g.P("Send(*", method.Input.GoIdent, ") error")
	}
	if genRecv {
		g.P("Recv() (*", method.Output.GoIdent, ", error)")
	}
	if genCloseAndRecv {
		g.P("CloseAndRecv() (*", method.Output.GoIdent, ", error)")
	}
	g.P(grpcPackage.Ident("ClientStream"))
	g.P("}")
	g.P()

	g.P("type ", streamType, " struct {")
	g.P(grpcPackage.Ident("ClientStream"))
	g.P("}")
	g.P()

	if genSend {
		g.P("func (x *", streamType, ") Send(m *", method.Input.GoIdent, ") error {")
		g.P("return x.ClientStream.SendMsg(m)")
		g.P("}")
		g.P()
	}
	if genRecv {
		g.P("func (x *", streamType, ") Recv() (*", method.Output.GoIdent, ", error) {")
		g.P("m := new(", method.Output.GoIdent, ")")
		g.P("if err := x.ClientStream.RecvMsg(m); err != nil { return nil, err }")
		g.P("return m, nil")
		g.P("}")
		g.P()
	}
	if genCloseAndRecv {
		g.P("func (x *", streamType, ") CloseAndRecv() (*", method.Output.GoIdent, ", error) {")
		g.P("if err := x.ClientStream.CloseSend(); err != nil { return nil, err }")
		g.P("m := new(", method.Output.GoIdent, ")")
		g.P("if err := x.ClientStream.RecvMsg(m); err != nil { return nil, err }")
		g.P("return m, nil")
		g.P("}")
		g.P()
	}
}

func genService(gen *protogen.Plugin, file *protogen.File, g *protogen.GeneratedFile, service *protogen.Service) {
	// Server struct.
	serviceType := service.GoName + "Service"
	g.P("// ", serviceType, " is the service API for ", service.GoName, " service.")
	g.P("// Fields should be assigned to their respective handler implementations only before")
	g.P("// Register", serviceType, " is called.  Any unassigned fields will result in the")
	g.P("// handler for that method returning an Unimplemented error.")
	if service.Desc.Options().(*descriptorpb.ServiceOptions).GetDeprecated() {
		g.P("//")
		g.P(deprecationComment)
	}
	g.Annotate(serviceType, service.Location)
	g.P("type ", serviceType, " struct {")
	for _, method := range service.Methods {
		if method.Desc.Options().(*descriptorpb.MethodOptions).GetDeprecated() {
			g.P(deprecationComment)
		}
		g.Annotate(serviceType+"."+method.GoName, method.Location)
		g.P(method.Comments.Leading,
			handlerSignature(g, method))
	}
	g.P("}")
	g.P()

	// Method handler implementations.
	for _, method := range service.Methods {
		genMethodHandler(gen, g, method)
	}

	// Stream interfaces and implementations.
	for _, method := range service.Methods {
		genServerStreamTypes(gen, g, method)
	}

	// Service registration.
	genRegisterFunction(gen, file, g, service)

	// Short-cut service constructor.
	genServiceConstructor(gen, g, service)
}

func genRegisterFunction(gen *protogen.Plugin, file *protogen.File, g *protogen.GeneratedFile, service *protogen.Service) {
	g.P("// Register", service.GoName, "Service registers a service implementation with a gRPC server.")
	if service.Desc.Options().(*descriptorpb.ServiceOptions).GetDeprecated() {
		g.P("//")
		g.P(deprecationComment)
	}
	g.P("func Register", service.GoName, "Service(s ", grpcPackage.Ident("ServiceRegistrar"), ", srv *", service.GoName, "Service) {")

	// Service descriptor.
	g.P("sd := ", grpcPackage.Ident("ServiceDesc"), " {")
	g.P("ServiceName: ", strconv.Quote(string(service.Desc.FullName())), ",")
	g.P("Methods: []", grpcPackage.Ident("MethodDesc"), "{")
	for _, method := range service.Methods {
		if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
			continue
		}
		g.P("{")
		g.P("MethodName: ", strconv.Quote(string(method.Desc.Name())), ",")
		g.P("Handler: srv.", unexport(method.GoName), ",")
		g.P("},")
	}
	g.P("},")
	g.P("Streams: []", grpcPackage.Ident("StreamDesc"), "{")
	for _, method := range service.Methods {
		if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
			continue
		}
		g.P("{")
		g.P("StreamName: ", strconv.Quote(string(method.Desc.Name())), ",")
		g.P("Handler: srv.", unexport(method.GoName), ",")
		if method.Desc.IsStreamingServer() {
			g.P("ServerStreams: true,")
		}
		if method.Desc.IsStreamingClient() {
			g.P("ClientStreams: true,")
		}
		g.P("},")
	}
	g.P("},")
	g.P("Metadata: \"", file.Desc.Path(), "\",")
	g.P("}")
	g.P()

	g.P("s.RegisterService(&sd, nil)")
	g.P("}")
	g.P()
}

func genServiceConstructor(gen *protogen.Plugin, g *protogen.GeneratedFile, service *protogen.Service) {
	g.P("// New", service.GoName, "Service creates a new ", service.GoName, "Service containing the")
	g.P("// implemented methods of the ", service.GoName, " service in s.  Any unimplemented")
	g.P("// methods will result in the gRPC server returning an UNIMPLEMENTED status to the client.")
	g.P("// This includes situations where the method handler is misspelled or has the wrong")
	g.P("// signature.  For this reason, this function should be used with great care and")
	g.P("// is not recommended to be used by most users.")
	g.P("func New", service.GoName, "Service(s interface{}) *", service.GoName, "Service {")
	g.P("ns := &", service.GoName, "Service{}")
	for _, method := range service.Methods {
		g.P("if h, ok := s.(interface {", methodSignature(g, method), "}); ok {")
		g.P("ns.", method.GoName, " = h.", method.GoName)
		g.P("}")
	}
	g.P("return ns")
	g.P("}")
	g.P()
}

func genUnstableServiceInterface(gen *protogen.Plugin, file *protogen.File, g *protogen.GeneratedFile, service *protogen.Service) {
	// Service interface.
	serviceType := service.GoName + "Service"
	g.P("// Unstable", serviceType, " is the service API for ", service.GoName, " service.")
	g.P("// New methods may be added to this interface if they are added to the service")
	g.P("// definition, which is not a backward-compatible change.  For this reason, ")
	g.P("// use of this type is not recommended.")
	if service.Desc.Options().(*descriptorpb.ServiceOptions).GetDeprecated() {
		g.P("//")
		g.P(deprecationComment)
	}
	g.Annotate("Unstable"+serviceType, service.Location)
	g.P("type Unstable", serviceType, " interface {")
	for _, method := range service.Methods {
		g.Annotate("Unstable"+serviceType+"."+method.GoName, method.Location)
		if method.Desc.Options().(*descriptorpb.MethodOptions).GetDeprecated() {
			g.P(deprecationComment)
		}
		g.P(method.Comments.Leading,
			methodSignature(g, method))
	}
	g.P("}")
	g.P()
}

func methodSignature(g *protogen.GeneratedFile, method *protogen.Method) string {
	var reqArgs []string
	ret := "error"
	if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
		reqArgs = append(reqArgs, g.QualifiedGoIdent(contextPackage.Ident("Context")))
		ret = "(*" + g.QualifiedGoIdent(method.Output.GoIdent) + ", error)"
	}
	if !method.Desc.IsStreamingClient() {
		reqArgs = append(reqArgs, "*"+g.QualifiedGoIdent(method.Input.GoIdent))
	}
	if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
		reqArgs = append(reqArgs, method.Parent.GoName+"_"+method.GoName+"Server")
	}
	return method.GoName + "(" + strings.Join(reqArgs, ", ") + ") " + ret
}

func handlerSignature(g *protogen.GeneratedFile, method *protogen.Method) string {
	var reqArgs []string
	ret := "error"
	if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
		reqArgs = append(reqArgs, g.QualifiedGoIdent(contextPackage.Ident("Context")))
		ret = "(*" + g.QualifiedGoIdent(method.Output.GoIdent) + ", error)"
	}
	if !method.Desc.IsStreamingClient() {
		reqArgs = append(reqArgs, "*"+g.QualifiedGoIdent(method.Input.GoIdent))
	}
	if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
		reqArgs = append(reqArgs, method.Parent.GoName+"_"+method.GoName+"Server")
	}
	return method.GoName + " func(" + strings.Join(reqArgs, ", ") + ") " + ret
}

func unaryHandlerSignature(g *protogen.GeneratedFile) string {
	return "(_ interface{}, ctx " + g.QualifiedGoIdent(contextPackage.Ident("Context")) +
		", dec func(interface{}) error, interceptor " + g.QualifiedGoIdent(grpcPackage.Ident("UnaryServerInterceptor")) + ") (interface{}, error)"
}

func streamHandlerSignature(g *protogen.GeneratedFile) string {
	return "(_ interface{}, stream " + g.QualifiedGoIdent(grpcPackage.Ident("ServerStream")) + ") error"
}

func genMethodHandler(gen *protogen.Plugin, g *protogen.GeneratedFile, method *protogen.Method) {
	service := method.Parent

	nilArg := ""
	signature := streamHandlerSignature(g)
	if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
		nilArg = "nil,"
		signature = unaryHandlerSignature(g)
	}
	g.P("func (s *", service.GoName, "Service) ", unexport(method.GoName), signature, " {")

	g.P("if s.", method.GoName, " == nil {")
	g.P("return ", nilArg, statusPackage.Ident("Errorf"), "(", codesPackage.Ident("Unimplemented"), `, "method `, method.GoName, ` not implemented")`)
	g.P("}")
	genHandlerBody(gen, g, method)

	g.P("}")
}

func genHandlerBody(gen *protogen.Plugin, g *protogen.GeneratedFile, method *protogen.Method) {
	service := method.Parent
	if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
		g.P("in := new(", method.Input.GoIdent, ")")
		g.P("if err := dec(in); err != nil { return nil, err }")
		g.P("if interceptor == nil { return s.", method.GoName, "(ctx, in) }")
		g.P("info := &", grpcPackage.Ident("UnaryServerInfo"), "{")
		g.P("Server: s,")
		g.P("FullMethod: ", strconv.Quote(fmt.Sprintf("/%s/%s", service.Desc.FullName(), method.GoName)), ",")
		g.P("}")
		g.P("handler := func(ctx ", contextPackage.Ident("Context"), ", req interface{}) (interface{}, error) {")
		g.P("return s.", method.GoName, "(ctx, req.(*", method.Input.GoIdent, "))")
		g.P("}")
		g.P("return interceptor(ctx, in, info, handler)")
		return
	}
	streamType := unexport(service.GoName) + method.GoName + "Server"
	if !method.Desc.IsStreamingClient() {
		// Server-streaming
		g.P("m := new(", method.Input.GoIdent, ")")
		g.P("if err := stream.RecvMsg(m); err != nil { return err }")
		g.P("return s.", method.GoName, "(m, &", streamType, "{stream})")
	} else {
		// Bidi-streaming
		g.P("return s.", method.GoName, "(&", streamType, "{stream})")
	}
}

func genServerStreamTypes(gen *protogen.Plugin, g *protogen.GeneratedFile, method *protogen.Method) {
	if *migrationMode {
		return
	}
	if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
		// Unary method
		return
	}
	service := method.Parent
	streamType := unexport(service.GoName) + method.GoName + "Server"
	genSend := method.Desc.IsStreamingServer()
	genSendAndClose := !method.Desc.IsStreamingServer()
	genRecv := method.Desc.IsStreamingClient()

	// Stream auxiliary types and methods.
	g.P("type ", service.GoName, "_", method.GoName, "Server interface {")
	if genSend {
		g.P("Send(*", method.Output.GoIdent, ") error")
	}
	if genSendAndClose {
		g.P("SendAndClose(*", method.Output.GoIdent, ") error")
	}
	if genRecv {
		g.P("Recv() (*", method.Input.GoIdent, ", error)")
	}
	g.P(grpcPackage.Ident("ServerStream"))
	g.P("}")
	g.P()

	g.P("type ", streamType, " struct {")
	g.P(grpcPackage.Ident("ServerStream"))
	g.P("}")
	g.P()

	if genSend {
		g.P("func (x *", streamType, ") Send(m *", method.Output.GoIdent, ") error {")
		g.P("return x.ServerStream.SendMsg(m)")
		g.P("}")
		g.P()
	}
	if genSendAndClose {
		g.P("func (x *", streamType, ") SendAndClose(m *", method.Output.GoIdent, ") error {")
		g.P("return x.ServerStream.SendMsg(m)")
		g.P("}")
		g.P()
	}
	if genRecv {
		g.P("func (x *", streamType, ") Recv() (*", method.Input.GoIdent, ", error) {")
		g.P("m := new(", method.Input.GoIdent, ")")
		g.P("if err := x.ServerStream.RecvMsg(m); err != nil { return nil, err }")
		g.P("return m, nil")
		g.P("}")
		g.P()
	}

	return
}

const deprecationComment = "// Deprecated: Do not use."

func unexport(s string) string { return strings.ToLower(s[:1]) + s[1:] }
