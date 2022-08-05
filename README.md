# storjscan

`STORJ` token payments service: `storjscan` manages wallet addresses and scans the blockchain for new STORJ token
transfer transactions for the managed wallet addresses. It also calculates the USD amount of the transfers based on
actual price.

## Build

```bash
go build -o build/ ./cmd/storjscan
```

## Test

Storjscan creates local POA blockchain network to test code that interacts with Ethereum.

Tests that require postgres DB are skipped by default.

To enable it `STORJ_POSTGRES_TEST` and/or `STORJ_COCKROACH_TEST` env should be set with postgres/cockroach connection strings pointing to test database(s).

```bash
export STORJ_POSTGRES_TEST=postgres://postgres@localhost/teststorjscan?sslmode=disable
export STORJ_COCKROACH_TEST=cockroach://root@localhost:26257/testcockroach?sslmode=disable
go test -race ./...
```

## Run

Requirements:

* Running blockchain
* Postgres (or Cockroach) database backend

Example parameters:

```bash
./build/storjscan migrate --database="postgres://postgres@localhost:5432/storjscandb?sslmode=disable"

./build/storjscan run \
--database="postgres://postgres@localhost:5432/storjscandb?sslmode=disable" \
--tokens.endpoint="https://mainnet.example-node.address" \
--tokens.token-address=0xB64ef51C888972c908CFacf59B47C1AfBC0Ab8aC \
--api.address="127.0.0.1:10000" \
--api.keys="eu1:eu1secret" \
--token-price.coinmarketcap-config.base-url="https://sandbox-api.coinmarketcap.com" \
--token-price.coinmarketcap-config.api-key="b54bcf4d-1bca-4e8e-9a24-22ff2c3d462c"
```
The separate migration step above is optional (`--with-migration` dev default is true)

## Local development run

Dev blockchain can be started with the help of the included docker-compose file:

*IF you need only storjscan* run the following:

Start the required services:

```
docker-compose up -d db 
docker-compose up -d geth
```

Initialize blockchain with data:

```
#This requires at least go 1.18, but you can also download binaries from release page
go install github.com/elek/cethacea@latest
export CETH_CHAIN=http://localhost:8545

#check your balance
cethacea contract deploy --name TOKEN contracts/build/TestToken.bin --abi contracts/build/TestToken.abi  '(uint256)' 1000000000000
cethacea token transfer 1000000000 key2
cethacea token transfer 1000000000 key2
cethacea token transfer 1000000000 key2
```

Note: contract creation should be the first transaction of the `key` account to get the same contract address what we use in the following examples.

Check the balance:

```
cethacea token balance --account=key1
cethacea token balance --account=key2
```

You can check the logs with:

```
cethacea contract logs
```

Now you have two options:

1. Run `storjscan` in your host machine
2. Run `storjscan` inside the docker cluster (requires valid cross compiled `storjscan` binary in `cmd/storjscan`))

If you prefer the first, you can start storj scan:

```bash
go run ./cmd/storjscan migrate --database="postgres://postgres@localhost:5432/storjscandb?sslmode=disable"

go run ./cmd/storjscan run \
--database="postgres://postgres@localhost:5432/storjscandb?sslmode=disable" \
--tokens.endpoint="http://localhost:8545" \
--tokens.contract=0x1E119A589270646585b044db12098B1e456a88Af \
--api.address="127.0.0.1:12000" \
--api.keys="eu1:eu1secret" \
--token-price.coinmarketcap-config.base-url="https://sandbox-api.coinmarketcap.com" \
--token-price.coinmarketcap-config.api-key="b54bcf4d-1bca-4e8e-9a24-22ff2c3d462c"
```
The separate migration step above is optional (`--with-migration` dev default is true)

If you prefer to run everything in docker, you need to cross-compile a static binary:

```
cd cmd/storjscan
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
cd -
docker-compose up -d storjscan
```

Or you can compile the binary in a docker container:

```
./scripts/cross-compile.sh
```

### Full storjscan

Storjscan also can be started together with full Storj cluster (storage nodes + satellite). This is similar to the 2nd approach in the previous
section (requires cross-compiled binary):

```
cd cmd/storjscan
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
cd -
docker-compose up -d
```

or 

```
./scripts/cross-compile.sh
```

And the same `cethacea` commands are required to deploy the contract.

## Usage

Get the current user:

```bash
curl -X GET -u "eu1:eu1secret" \
http://127.0.0.1:12000/api/v0/auth/whoami
```

Get payments of random Ethereum address `0x69A0a76DaB9CE2bB2BDb3ba129eEd79606b4C2C6`

```bash
curl -X GET -u "eu1:eu1secret" \
http://127.0.0.1:12000/api/v0/tokens/payments/0xeD59a3C3426aB7eBDbD08005521Ab8084FA2e29c
```

Output

```json
[
  {
    "From": "0xdfd5293d8e347dfe59e90efd55b2956a1343963d",
    "TokenValue": 45246900000,
    "BlockHash": "0xdd782ac418835fe5f80ec3c32fd4ee595286bbc14c36f5f4f2b12c83df38d89a",
    "BlockNumber": 13361611,
    "Transaction": "0x451b6ef600db4f059d2b792b524c7de5eee837631266f8cdc53997098723f438",
    "LogIndex": 43
  }
]
```

## Contracts

Storjscan uses `abigen` tool to generate `ERC20` interface and `TestToken` contract go bindings. To generate contract go
bindings `contracts/TestToken.sol` should be compiled first.

### TestToken

`TestToken` is test `ERC20` token implementation which is deployed to a local test network and used for testing
purposes. Source code of `TestToken` is located at `contract/TestToken.sol`.

For the sake of easier testing the compiled binaries/abi are also included in `contract/build/TestTokeb.bin`, but the
contract can be recompiled with `solc` (Solidity compiler):

Prior to compiling token source code `openzeppelin` contract library should be installed via `npm install`.

```bash
# TestToken contract compilation
pushd contracts/
npm install
./compile.sh
popd 
```

`TestToken` go contract bindings are located at `private/testeth` pkg.

```bash
# generate TestToken contract go bindings
go generate ./private/testeth
```

### ERC20 contract go bindings

`ERC20` bindings are generated from `openzeppelin` `ERC20` token implementation ABI and located under `tokens/erc20` pkg.
It is used to interact with on-chain `STORJ` token contract to retrieve `Transfer` events of a deposit wallet address.

```bash
# generate ERC20 contract go bindings
go generate ./tokens
```
