// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"storj.io/common/errs2"
)

// Error is an error class for API http server error.
var Error = errs.Class("api server")

// Config holds API endpoint configuration.
type Config struct {
	Address string   `help:"public address to listen on" default:":10000"`
	Keys    []string `help:"List of secrets to connect to service endpoints."`
}

// Server represents storjscan API web server.
//
// architecture: Endpoint
type Server struct {
	log      *zap.Logger
	apiKeys  [][]byte
	listener net.Listener
	router   *mux.Router
	http     http.Server
}

// NewServer creates new API server instance.
func NewServer(log *zap.Logger, listener net.Listener, apiKeys [][]byte) *Server {
	router := mux.NewRouter()
	router.Name("api").PathPrefix("/api/v0")

	return &Server{
		log:      log,
		apiKeys:  apiKeys,
		listener: listener,
		router:   router,
		http: http.Server{
			Handler: router,
		},
	}
}

// NewAPI creates new API route and register endpoint methods.
func (server *Server) NewAPI(path string, register func(*mux.Router)) {
	apiRouter := server.router.GetRoute("api").Subrouter()
	router := apiRouter.PathPrefix(path).Subrouter()
	router.StrictSlash(true)
	apiRouter.Use(server.authorize)
	register(router)
}

// Run runs the server that host api endpoints.
func (server *Server) Run(ctx context.Context) (err error) {
	ctx, cancel := context.WithCancel(ctx)

	var group errgroup.Group
	group.Go(func() error {
		<-ctx.Done()
		return Error.Wrap(server.http.Shutdown(context.Background()))
	})
	group.Go(func() error {
		defer cancel()
		err := Error.Wrap(server.http.Serve(server.listener))
		if errs2.IsCanceled(err) || errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return err
	})

	return Error.Wrap(group.Wait())
}

// authorize validates request authorization using the provided api key found in the request header.
func (server *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, err := base64.URLEncoding.DecodeString(r.Header.Get("STORJSCAN_API_KEY"))
		if err != nil {
			server.serveJSONError(w, http.StatusUnauthorized, Error.Wrap(err))
			return
		}
		if !server.verifyAPIKey(apiKey) {
			server.serveJSONError(w, http.StatusUnauthorized, Error.New("invalid api key provided"))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// verifyAPIKey determines if the api key provided is valid.
func (server *Server) verifyAPIKey(apiKey []byte) bool {
	for _, validKey := range server.apiKeys {
		if subtle.ConstantTimeCompare(apiKey, validKey) == 1 {
			return true
		}
	}
	return false
}

// serveJSONError writes JSON error to response output stream.
func (server *Server) serveJSONError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)

	var response struct {
		Error string `json:"error"`
	}
	response.Error = err.Error()

	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		server.log.Error("failed to write json error response", zap.Error(Error.Wrap(err)))
		return
	}
}

// Close closes server and underlying listener.
func (server *Server) Close() error {
	return Error.Wrap(server.http.Close())
}
