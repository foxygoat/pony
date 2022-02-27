// Package serve implements the "jig serve" command, serving GRPC
// services via an evaluator.
package serve

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"

	"foxygo.at/jig/log"
	"foxygo.at/jig/reflection"
	"foxygo.at/jig/registry"
	"foxygo.at/jig/serve/httprule"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Option is a functional option to configure Server
type Option func(s *Server) error

func WithProtosets(protosets ...string) Option {
	return func(s *Server) error {
		s.protosets = append(s.protosets, protosets...)
		return nil
	}
}

func WithLogger(logger log.Logger) Option {
	return func(s *Server) error {
		s.log = logger
		return nil
	}
}

type Server struct {
	log       log.Logger
	gs        *grpc.Server
	http      *httprule.Server
	files     *registry.Files
	fs        fs.FS
	protosets []string
	eval      Evaluator
}

// NewServer creates a new Server for given evaluator, e.g. Jsonnet and
// data Directories.
func NewServer(eval Evaluator, vfs fs.FS, options ...Option) (*Server, error) {
	s := &Server{
		files: new(registry.Files),
		log:   log.NewLogger(os.Stderr, log.LogLevelError),
		eval:  eval,
		fs:    vfs,
	}
	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	if err := s.loadProtosets(); err != nil {
		return nil, err
	}
	s.http = httprule.NewServer(s.files, s.callMethod)
	return s, nil
}

func (s *Server) Serve(lis net.Listener) error {
	s.gs = grpc.NewServer(grpc.UnknownServiceHandler(s.UnknownHandler))
	reflection.NewService(&s.files.Files).Register(s.gs)
	return http.Serve(lis, h2c.NewHandler(s, &http2.Server{}))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		s.gs.ServeHTTP(w, r)
		return
	}
	s.http.ServeHTTP(w, r)
}

func (s *Server) ListenAndServe(listenAddr string) error {
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	s.log.Infof("Listening on %s", listenAddr)
	return s.Serve(l)
}

func (s *Server) Stop() {
	if s.gs != nil {
		s.gs.GracefulStop()
	}
}

func (s *Server) loadProtosets() error {
	seen := map[string]bool{}
	for _, protoset := range s.protosets {
		s.log.Debugf("loading protoset file: %s", protoset)
		b, err := os.ReadFile(protoset)
		if err != nil {
			return err
		}
		if err := s.addFiles(b, seen); err != nil {
			return err
		}
	}

	matches, err := fs.Glob(s.fs, "*.pb")
	if err != nil {
		return err
	}
	for _, match := range matches {
		if strings.HasPrefix(match, "_") {
			continue
		}
		s.log.Debugf("loading discovered protoset file: %s", match)
		b, err := fs.ReadFile(s.fs, match)
		if err != nil {
			return err
		}
		if err := s.addFiles(b, seen); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) addFiles(b []byte, seen map[string]bool) error {
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(b, fds); err != nil {
		return err
	}
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return err
	}
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if seen[fd.Path()] {
			return true
		}
		seen[fd.Path()] = true
		s.log.Debugf("loading file descriptor %s", fd.Path())
		err := s.files.RegisterFile(fd)
		if err != nil {
			s.log.Errorf("cannot register %q: %v", fd.FullName(), err)
		}
		return true
	})
	return nil
}

func (s *Server) lookupMethod(name protoreflect.FullName) protoreflect.MethodDescriptor {
	desc, err := s.files.FindDescriptorByName(name)
	if err != nil {
		return nil
	}
	md, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return nil
	}
	return md
}

// UnknownHandler handles gRPC requests that are not statically implemented.
//
// This is the main entrypoint of the jig server, being a dynamic gRPC server.
// It implements grpc.StreamHandler, although it ignores the first argument as
// that is the concrete implementation of the server, and for dynamic
// implementations and the unknown handler, this is nil.
//
// It is expected that the context returned from ss.Context() can be used
// with grpc.ServerTransportStreamFromContext() to return a
// grpc.ServerTransportStream interface for its `Method()` method.
func (s *Server) UnknownHandler(_ interface{}, ss grpc.ServerStream) error {
	method, ok := grpc.Method(ss.Context())
	if !ok {
		return status.Errorf(codes.Internal, "no method in stream context")
	}

	s.log.Debugf("%s: new request", method)

	// Convert /pkg.service/method -> pkg.service.method
	fullMethod := protoreflect.FullName(strings.ReplaceAll(method[1:], "/", "."))
	md := s.lookupMethod(fullMethod)
	if md == nil {
		s.log.Warnf("%s: method not found", fullMethod)
		return status.Errorf(codes.Unimplemented, "method not found: %s", fullMethod)
	}

	err := s.callMethod(md, ss)
	if err != nil {
		s.log.Errorf("%s: %s", method, err)
	}
	return err
}

type TestServer struct {
	Server
	lis net.Listener
}

// NewTestServer starts and returns a new TestServer.
// The caller should call Stop when finished, to shut it down.
func NewTestServer(eval Evaluator, vfs fs.FS, options ...Option) *TestServer {
	s, err := NewServer(eval, vfs, options...)
	if err != nil {
		panic(fmt.Sprintf("failed to create TestServer: %v", err))
	}
	ts := &TestServer{Server: *s}
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(fmt.Sprintf("TestServer failed to listen: %v", err))
	}
	ts.lis = l
	go ts.Serve(l) //nolint: errcheck
	return ts
}

func (ts *TestServer) Addr() string {
	return ts.lis.Addr().String()
}
