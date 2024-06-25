-- AUTOGENERATED BY storj.io/dbx
-- DO NOT EDIT
CREATE TABLE block_headers (
	chain_id bigint NOT NULL,
	hash bytea NOT NULL,
	number bigint NOT NULL,
	timestamp timestamp with time zone NOT NULL,
	created_at timestamp with time zone NOT NULL DEFAULT current_timestamp,
	PRIMARY KEY ( chain_id, hash )
);
CREATE TABLE token_prices (
	interval_start timestamp with time zone NOT NULL,
	price bigint NOT NULL,
	PRIMARY KEY ( interval_start )
);
CREATE TABLE transfer_events (
	chain_id bigint NOT NULL,
	block_hash bytea NOT NULL,
	block_number bigint NOT NULL,
	transaction bytea NOT NULL,
	log_index integer NOT NULL,
	from_address bytea NOT NULL,
	to_address bytea NOT NULL,
	token_value bigint NOT NULL,
	created_at timestamp with time zone NOT NULL DEFAULT current_timestamp,
	PRIMARY KEY ( chain_id, block_hash, log_index )
);
CREATE TABLE wallets (
	id bigserial NOT NULL,
	address bytea NOT NULL,
	claimed timestamp with time zone,
	satellite text NOT NULL,
	info text,
	created_at timestamp with time zone NOT NULL DEFAULT current_timestamp,
	PRIMARY KEY ( id )
);
CREATE INDEX wallets_satellite_index ON wallets ( satellite ) ;
CREATE UNIQUE INDEX wallets_address_unique_index ON wallets ( address ) ;
