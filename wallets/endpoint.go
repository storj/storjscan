// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"storj.io/storjscan/tokens"
)

// ErrEndpoint is the wallets endpoint error class.
var ErrEndpoint = errs.Class("Wallets Endpoint")

// Endpoint for interacting with the Wallets service.
//
// architecture: Endpoint
type Endpoint struct {
	log     *zap.Logger
	service Wallets
}

// NewEndpoint creates new wallets endpoint instance.
func NewEndpoint(log *zap.Logger, wallets Wallets) *Endpoint {
	return &Endpoint{
		log:     log,
		service: wallets,
	}
}

// Register registers endpoint methods on API server subroute.
func (endpoint *Endpoint) Register(router *mux.Router) {
	router.HandleFunc("/wallets", endpoint.GetNewDepositAddress).Methods(http.MethodGet)
	router.HandleFunc("/wallets/count", endpoint.GetCountDepositAddresses).Methods(http.MethodGet)
	router.HandleFunc("/wallets/count/claimed", endpoint.GetCountClaimedDepositAddresses).Methods(http.MethodGet)
	router.HandleFunc("/wallets/count/unclaimed", endpoint.GetCountUnclaimedDepositAddresses).Methods(http.MethodGet)
	router.HandleFunc("/wallets/{address}", endpoint.GetAccount).Methods(http.MethodGet)
}

// GetNewDepositAddress returns a new deposit address.
func (endpoint *Endpoint) GetNewDepositAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	address, err := endpoint.service.GetNewDepositAddress(ctx)
	if err != nil {
		endpoint.serveJSONError(w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(address)
	if err != nil {
		endpoint.log.Error("failed to write json wallets response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}

// GetCountDepositAddresses returns the total number of deposit addresses in the storjscan database.
func (endpoint *Endpoint) GetCountDepositAddresses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	count, err := endpoint.service.GetCountDepositAddresses(ctx)
	if err != nil {
		endpoint.serveJSONError(w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(count)
	if err != nil {
		endpoint.log.Error("failed to write json wallets response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}

// GetCountClaimedDepositAddresses returns the  number of claimed deposit addresses in the storjscan database.
func (endpoint *Endpoint) GetCountClaimedDepositAddresses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	count, err := endpoint.service.GetCountClaimedDepositAddresses(ctx, true)
	if err != nil {
		endpoint.serveJSONError(w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(count)
	if err != nil {
		endpoint.log.Error("failed to write json wallets response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}

// GetCountUnclaimedDepositAddresses returns the  number of unclaimed deposit addresses in the storjscan database.
func (endpoint *Endpoint) GetCountUnclaimedDepositAddresses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	count, err := endpoint.service.GetCountClaimedDepositAddresses(ctx, false)
	if err != nil {
		endpoint.serveJSONError(w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(count)
	if err != nil {
		endpoint.log.Error("failed to write json wallets response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}

// GetAccount returns available info about the address provided.
func (endpoint *Endpoint) GetAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)
	addressHex := mux.Vars(r)["address"]

	address, err := tokens.AddressFromHex(addressHex)
	if err != nil {
		endpoint.serveJSONError(w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	account, err := endpoint.service.GetAccount(ctx, address.Bytes())
	if err != nil {
		endpoint.serveJSONError(w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(account)
	if err != nil {
		endpoint.log.Error("failed to write json wallets response", zap.Error(ErrEndpoint.Wrap(err)))
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
