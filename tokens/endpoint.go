// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
)

// ErrEndpoint - tokens endpoint error class.
var ErrEndpoint = errs.Class("tokens endpoint")

// Endpoint for querying ERC20 token information from ethereum chain.
//
// architecture: Endpoint
type Endpoint struct {
	log     *zap.Logger
	service *Service
}

// NewEndpoint creates new payments endpoint instance.
func NewEndpoint(log *zap.Logger, service *Service) *Endpoint {
	return &Endpoint{
		log:     log,
		service: service,
	}
}

// Register registers endpoint methods on API server subroute.
func (endpoint *Endpoint) Register(router *mux.Router) {
	router.HandleFunc("/payments/{address}", endpoint.Payments).Methods(http.MethodGet)
}

// Payments endpoint retrieves all ERC20 token payments for ethereum address.
func (endpoint *Endpoint) Payments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	addressHex := mux.Vars(r)["address"]

	address, err := AddressFromHex(addressHex)
	if err != nil {
		endpoint.serveJSONError(w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	payments, err := endpoint.service.Payments(ctx, address)
	if err != nil {
		endpoint.serveJSONError(w, http.StatusInternalServerError, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(payments)
	if err != nil {
		endpoint.log.Error("failed to write json payments response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}

// serveJSONError writes JSON error to response output stream.
func (endpoint *Endpoint) serveJSONError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)

	var response struct {
		Error string `json:"error"`
	}

	response.Error = err.Error()

	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		endpoint.log.Error("failed to write json error response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}
