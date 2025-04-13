package http

import (
	"context"
	"github.com/fluxsets/dyno"
	"gocloud.dev/server"
	"log/slog"
	"net/http"
)

func NewRouter() *http.ServeMux {
	return http.NewServeMux()
}

func NewServer(addr string, h http.HandlerFunc) *Server {
	hs := server.New(h, &server.Options{
		Driver: server.NewDefaultDriver(),
	})
	return &Server{
		Server: hs,
		addr:   addr,
	}
}

type Server struct {
	*server.Server
	addr   string
	logger *slog.Logger
}

func (s *Server) ID() string {
	return "http"
}

func (s *Server) Init(do dyno.Dyno) error {
	s.logger = do.Logger("deployment", s.ID())
	return nil
}

func (s *Server) Start(ctx context.Context) error {
	return s.Server.ListenAndServe(s.addr)
}

func (s *Server) Stop(ctx context.Context) {
	if err := s.Server.Shutdown(ctx); err != nil {
		s.logger.Warn("Error shutting down http server", "error", err)
	}
}

var _ dyno.ServerLike = (*Server)(nil)
