// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package coinmarketcap

// status is the status structure for the coinmarketcap api.
type status struct {
	Timestamp    string `json:"timestamp"`
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Elapsed      int    `json:"elapsed"`
	CreditCount  int    `json:"credit_count"`
}

// quoteLatestResponse is the response structure from the coinmarketcap api for the latest data.
type quoteLatestResponse struct {
	Status status                     `json:"status"`
	Data   map[string]quoteLatestData `json:"data"`
}

// quoteLatestData struct contains the latest quote map as well as metadata.
type quoteLatestData struct {
	ID     float64                `json:"id"`
	Name   string                 `json:"name"`
	Symbol string                 `json:"symbol"`
	Quote  map[string]latestQuote `json:"quote"`
}

// latestQuote is the quote structure for the latest data.
type latestQuote struct {
	Price       float64 `json:"price"`
	LastUpdated string  `json:"last_updated"`
}

// QuoteHistoricResponse is the response structure from the coinmarketcap api for historic data.
type quoteHistoricResponse struct {
	Status status                       `json:"status"`
	Data   map[string]quoteHistoricData `json:"data"`
}

// QuoteHistoricData struct contains historic quote map as well as metadata.
type quoteHistoricData struct {
	ID     float64          `json:"id"`
	Name   string           `json:"name"`
	Symbol string           `json:"symbol"`
	Quotes []historicQuotes `json:"quotes"`
}

// HistoricQuotes is the quote history map.
type historicQuotes struct {
	Quote map[string]historicQuote `json:"quote"`
}

// HistoricQuote is the quote structure for historical data.
type historicQuote struct {
	Price     float64 `json:"price"`
	Timestamp string  `json:"timestamp"`
}
