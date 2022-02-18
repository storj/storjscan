// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
)

// ErrEndpoint is the wallets endpoint error class.
var ErrEndpoint = errs.Class("tokens endpoint")

// Endpoint for interacting with the Wallets service.
//
// architecture: Endpoint
type Endpoint struct {
	log     *zap.Logger
	wallets *Wallets
}

// NewEndpoint creates new wallets endpoint instance.
func NewEndpoint(log *zap.Logger, wallets *Wallets) *Endpoint {
	return &Endpoint{
		log:     log,
		wallets: wallets,
	}
}

func (endpoint *Endpoint) Register(router *mux.Router) {
	router.HandleFunc("/wallets", endpoint.NewDepositAddress).Methods(http.MethodGet)
}

// NewDepositAddress ...
// WARNING: TEST USE ONLY! SECURITY NOT IMPLEMENTED
func(endpoint *Endpoint) NewDepositAddress(w http.ResponseWriter, r *http.Request){
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)
	//call endpoint.service.newDepositAddress

}


