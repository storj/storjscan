// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storjscan/api"
	"storj.io/storjscan/blockchain"
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
	router.HandleFunc("/payments", endpoint.AllPayments).Methods(http.MethodGet)
}

// Payments endpoint retrieves all ERC20 token payments of one specific wallet, starting from particular block for ethereum address.
func (endpoint *Endpoint) Payments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	addressHex := mux.Vars(r)["address"]

	address, err := blockchain.AddressFromHex(addressHex)
	if err != nil {
		api.ServeJSONError(endpoint.log, w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
		return
	}

	chainIds, err := endpoint.service.GetChainIds(ctx)
	from := map[int64]int64{}
	for chainID := range chainIds {
		from[chainID] = 0
		if s := r.URL.Query().Get(strconv.FormatInt(chainID, 10)); s != "" {
			block, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				api.ServeJSONError(endpoint.log, w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
				return
			}
			from[chainID] = block
		}
	}

	payments, err := endpoint.service.Payments(ctx, address, from)
	if err != nil {
		api.ServeJSONError(endpoint.log, w, http.StatusInternalServerError, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(payments)
	if err != nil {
		endpoint.log.Error("failed to write json payments response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}

// AllPayments endpoint retrieves all ERC20 token payments claimed by one satellite starting from particular block for ethereum address.
func (endpoint *Endpoint) AllPayments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	chainIds, err := endpoint.service.GetChainIds(ctx)
	from := map[int64]int64{}
	for chainID := range chainIds {
		from[chainID] = 0
		if s := r.URL.Query().Get(strconv.FormatInt(chainID, 10)); s != "" {
			block, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				api.ServeJSONError(endpoint.log, w, http.StatusBadRequest, ErrEndpoint.Wrap(err))
				return
			}
			from[chainID] = block
		}
	}

	// We request logs of 100 addresses in one batch. We can make it configurable if required later.
	payments, err := endpoint.service.AllPayments(ctx, api.GetAPIIdentifier(ctx), from)
	if err != nil {

		api.ServeJSONError(endpoint.log, w, http.StatusInternalServerError, ErrEndpoint.Wrap(err))
		return
	}

	err = json.NewEncoder(w).Encode(payments)
	if err != nil {
		endpoint.log.Error("failed to write json payments response", zap.Error(ErrEndpoint.Wrap(err)))
		return
	}
}
