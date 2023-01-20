// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storjscan/api"
)

// ErrEndpoint is the wallets endpoint error class.
var ErrEndpoint = errs.Class("Wallets Endpoint")

// Endpoint for interacting with the Wallets service.
//
// architecture: Endpoint
type Endpoint struct {
	log     *zap.Logger
	service *Service
}

// NewEndpoint creates new wallets endpoint instance.
func NewEndpoint(log *zap.Logger, service *Service) *Endpoint {
	return &Endpoint{
		log:     log,
		service: service,
	}
}

// Register registers endpoint methods on API server subroute.
func (endpoint *Endpoint) Register(router *mux.Router) {
	router.HandleFunc("/claim", endpoint.Claim).Methods(http.MethodPost)
	router.HandleFunc("/", endpoint.AddWallets).Methods(http.MethodPost)
}

// Claim returns an available deposit address.
func (endpoint *Endpoint) Claim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	satellite := api.GetAPIIdentifier(ctx)
	address, err := endpoint.service.Claim(ctx, satellite)

	if err != nil {
		api.ServeJSONError(endpoint.log, w, http.StatusInternalServerError, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(address.Hex())
	if err != nil {
		endpoint.log.Error("failed to write json wallets response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}

// AddWallets saves newly generated wallets.
func (endpoint *Endpoint) AddWallets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	var inserts []InsertWallet

	err = json.NewDecoder(r.Body).Decode(&inserts)
	if err != nil {
		api.ServeJSONError(endpoint.log, w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	satellite := api.GetAPIIdentifier(ctx)

	err = endpoint.service.Register(ctx, satellite, inserts)

	if err != nil {
		api.ServeJSONError(endpoint.log, w, http.StatusInternalServerError, ErrEndpoint.Wrap(err))
		return
	}

	w.WriteHeader(http.StatusOK)
}
