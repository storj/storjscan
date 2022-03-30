// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package api

import (
	"context"
	"crypto/subtle"
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
	Keys    []string `help:"List of user:secret pairs to connect to service endpoints."`
}

// Server represents storjscan API web server.
//
// architecture: Endpoint
type Server struct {
	log      *zap.Logger
	apiKeys  map[string]string
	listener net.Listener
	router   *mux.Router
	http     http.Server
}

// NewServer creates new API server instance.
func NewServer(log *zap.Logger, listener net.Listener, apiKeys map[string]string) *Server {
	router := mux.NewRouter()
	apiRouter := router.Name("api").PathPrefix("/api/v0").Subrouter()

	server := &Server{
		log:      log,
		apiKeys:  apiKeys,
		listener: listener,
		router:   apiRouter,
		http: http.Server{
			Handler: router,
		},
	}
	server.NewAPI("/auth", func(router *mux.Router) {
		router.HandleFunc("/whoami", whoami)
	})

	return server
}

func whoami(writer http.ResponseWriter, request *http.Request) {
	id := getAPIIdentifier(request.Context())
	if id == "" {
		// shouldn't be possible as all request are authenticated
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	response := struct {
		ID string
	}{
		ID: id,
	}
	err := json.NewEncoder(writer).Encode(response)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
}

// NewAPI creates new API route and register endpoint methods.
func (server *Server) NewAPI(path string, register func(*mux.Router)) {
	router := server.router.PathPrefix(path).Subrouter()
	router.StrictSlash(true)
	router.Use(server.authorize)
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

var apiID struct{}

// authorize validates request authorization using the provided api key found in the request header.
func (server *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok {
			w.Header().Add("www-Authenticate", "Basic realm=storjscan")
			server.serveJSONError(w, http.StatusUnauthorized, Error.New("authentication is required"))
			return
		}

		identity, found := server.verifyAPIKey(id, secret)
		if !found {
			server.serveJSONError(w, http.StatusUnauthorized, Error.New("invalid api key provided"))
			return
		}

		ctx := context.WithValue(r.Context(), apiID, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// getAPIIdentifier return the authenticated identity of the client.
func getAPIIdentifier(ctx context.Context) string {
	value := ctx.Value(apiID)
	if value == nil {
		return ""
	}
	return value.(string)
}

// verifyAPIKey determines if the api key provided is valid.
func (server *Server) verifyAPIKey(providedID string, providedSecret string) (apiID string, found bool) {
	for id, secret := range server.apiKeys {
		if subtle.ConstantTimeCompare([]byte(providedID), []byte(id))+subtle.ConstantTimeCompare([]byte(providedSecret), []byte(secret)) == 2 {
			apiID = id
			found = true
		}
	}
	return apiID, found
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

// LogRoutes print out registered routes to the log.
func (server *Server) LogRoutes() error {
	return server.router.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		template, err := route.GetPathTemplate()
		if err != nil {
			return err
		}
		methods, _ := route.GetMethods()
		server.log.Info("Rest endpoint is registered", zap.String("path", template), zap.Error(err), zap.Strings("methods", methods))
		return nil
	})
}
