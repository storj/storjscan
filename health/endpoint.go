// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package health

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/spacemonkeygo/monkit/v3"
	"go.uber.org/zap"

	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/tokens"
)

var mon = monkit.Package()

// Endpoint for liveness and readiness checks.
//
// architecture: Endpoint
type Endpoint struct {
	log          *zap.Logger
	db           Pingable
	tokenPrice   *tokenprice.Service
	tokenService *tokens.Service
}

// Pingable allows access to the storjscandb.
type Pingable interface {
	Ping(ctx context.Context) error
}

// NewEndpoint creates a new endpoint instance for the health checker.
func NewEndpoint(log *zap.Logger, db Pingable, tokenPrice *tokenprice.Service, tokenService *tokens.Service) *Endpoint {
	return &Endpoint{
		log:          log,
		db:           db,
		tokenPrice:   tokenPrice,
		tokenService: tokenService,
	}
}

// Register registers endpoint methods on API server subroute.
func (endpoint *Endpoint) Register(router *mux.Router) {
	router.HandleFunc("/live", endpoint.Live).Methods(http.MethodGet)
	router.HandleFunc("/ready", endpoint.Ready).Methods(http.MethodGet)
}

// Live checks if the storjscan service is running.
func (endpoint *Endpoint) Live(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// Ready checks whether the database connection is available and whether the token price and blockchain services are reachable.
// Returns 503 if database is unreachable. Sends a metric if either token price or blockchain services are unreachable.
// 200 indicates that the storjscan service is ready for use.
func (endpoint *Endpoint) Ready(w http.ResponseWriter, r *http.Request) {
	var ctx context.Context
	var err error
	status := http.StatusOK
	message := ""

	// test db
	if err = endpoint.db.Ping(ctx); err != nil {
		status = http.StatusServiceUnavailable
		message += "db:failure\n"
		mon.Event("health-db-failure")
		endpoint.log.Error(fmt.Sprintf("db failure: %s", err.Error()))
	} else {
		message += "db:ok\n"
	}

	// test token price service
	sc, err := endpoint.tokenPrice.Ping(ctx)
	if sc != http.StatusOK || err != nil {
		message += "tokenprice:failure\n"
		mon.Event("health-tokenprice-failure")
		endpoint.log.Error(fmt.Sprintf("tokenprice failure: %d\n", sc))
		if err != nil {
			mon.Event("health-tokenprice-error")
			endpoint.log.Error(fmt.Sprintf("tokenprice error: %s\n", err.Error()))
		}
	} else {
		message += "tokenprice:ok\n"
	}

	// test blockchain service
	if err = endpoint.tokenService.Ping(ctx); err != nil {
		message += "blockchain:failure\n"
		mon.Event("health-blockchain-failure")
		endpoint.log.Error(fmt.Sprintf("blockchain failure: %s\n", err.Error()))
	} else {
		message += "blockchain:ok\n"
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, err = w.Write([]byte(message))
	if err != nil {
		endpoint.log.Error(fmt.Sprintf("response writer error: %s\n", err.Error()))
	}
}
