# storjscan

`STORJ` token payments service: `storjscan` manages wallet addresses and scans the blockchain for new STORJ token
transfer transactions for the managed wallet addresses. It also calculates the USD amount of the transfers based on
actual price.

## Build

```bash
go build -o build/ ./cmd/storjscan
```

## Unit Tests

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
--api.keys="us1:us1secret" \
--token-price.coinmarketcap-config.base-url="https://sandbox-api.coinmarketcap.com" \
--token-price.coinmarketcap-config.api-key="b54bcf4d-1bca-4e8e-9a24-22ff2c3d462c"
```
The separate migration step above is optional (`--with-migration` dev default is true)

## Docker run

*If you wish to run storjscan and supporting services in docker* run the following:

Start the services:

```
docker-compose up -d
```

*If you wish to run everything, including storjscan as well as the satellite and supporting services in docker* (requires storj-up) run the following:

```
storj-up init storj,db,billing
docker-compose up -d
```

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
#This requires at least go 1.20, but you can also download binaries from release page
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
2. Run `storjscan` inside the docker cluster (requires valid cross compiled `storjscan` binary in `cmd/storjscan`)

If you prefer the first, you can start storj scan:

```bash
go run ./cmd/storjscan migrate --database="postgres://postgres@localhost:5432/storjscandb?sslmode=disable"

go run ./cmd/storjscan run \
--database="postgres://postgres@localhost:5432/storjscandb?sslmode=disable" \
--tokens.endpoint="http://localhost:8545" \
--tokens.contract=0x1E119A589270646585b044db12098B1e456a88Af \
--api.address="127.0.0.1:12000" \
--api.keys="us1:us1secret" \
--token-price.coinmarketcap-config.base-url="https://sandbox-api.coinmarketcap.com" \
--token-price.coinmarketcap-config.api-key="b54bcf4d-1bca-4e8e-9a24-22ff2c3d462c"
```
The separate migration step above is optional (`--with-migration` dev default is true)

If you prefer to use docker for testing local development, you need to cross-compile a static binary,
then mount it in the container (use storj-up to help with this):

```
cd cmd/storjscan
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
cd ../../
storj-up local storjscan -d <path to binary>
docker-compose up -d storjscan
```

Or you can compile the binary in a docker container:

```
./scripts/cross-compile.sh
```

### Local development full system

Storjscan also can be started together with full Storj cluster (storage nodes + satellite). This is similar to the 2nd approach in the previous
section (requires storj-up and cross-compiled binary):

```
storj-up init storj,db,billing
cd cmd/storjscan
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
cd ../../
storj-up local storjscan -d <path to binary>
docker-compose up -d storjscan
```

or 

```
./scripts/cross-compile.sh
```

And the same `cethacea` commands are required to deploy the contract.

## Usage

Generate new wallets:

```bash
storjscan mnemonic > .mnemonic
storjscan generate --api-key us1 --api-secret us1secret --address http://127.0.0.1:12000
```

Get the current user:

```bash
curl -X GET -u "us1:us1secret" \
http://127.0.0.1:12000/api/v0/auth/whoami
```

Get payments of random Ethereum address `0x69A0a76DaB9CE2bB2BDb3ba129eEd79606b4C2C6`

```bash
curl -X GET -u "us1:us1secret" \
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

If you have started the full system, you can also query the satellite for wallet and billing info. This requires a valid user account, and a session cookie to use with curl commands.

Create a default user and get a valid cookie
```bash
storj-up credentials
```
The cookie should print out along with the username, password, access grant and other user metadata.

Claim Wallet (requires generating wallets with mnemonic first)
```bash
curl --location --request POST 'http://localhost:10000/api/v0/payments/wallet' --header 'Cookie: <YOUR TOKEN KEY>'
```

sample output
```json
[
  {"address":"001bbf.....fd5182","balance":"0"}
]
```

Transfer tokens to claimed wallet
```bash
cethacea token transfer 1000 <address claimed above>
```
note that only confirmed transfers will reflect in a users balance. TO confirm a transfer, it needs several transfers or other block chain transactions to occur after it. You cse a loop here to send several transfers at once i.e.
```bash
for i in {1..15}; do cethacea token transfer 1000 <address claimed above>; done
```

sample output (transaction hash)
```
0xc9312c....3456d334
```

check token balance (may require several token transfers)
```bash
curl --location --request GET 'http://localhost:10000/api/v0/payments/wallet' --header 'Cookie: <YOUR TOKEN KEY>'
```

sample output
```json
{"address":"001bbf.....fd5182","balance":"576.502143"}
```

check token transactions
```bash
curl -sb GET http://localhost:10000/api/v0/payments/wallet/payments --header 'Cookie: <YOUR TOKEN KEY>'
```

sample output
```json
"payments": [{
    "ID": "0ff424af84334e172f1ce52adfde7a437ad49982f712cd3c961c8860ff412e36#0",
    "Type": "storjscan",
    "Wallet": "003bbdb149e6aa3a6e8f31b469379cea19559827",
    "Amount": {
      "value": "296.523254",
      "currency": "USDMicro"
    },
    "Received": {
      "value": "0",
      "currency": "USDMicro"
    },
    "Status": "pending",
    "Link": "https://etherscan.io/tx/0ff424af84334e172f1ce52adfde7a437ad49982f712cd3c961c8860ff412e36",
    "Timestamp": "2022-08-31T18:39:14Z"
  }, {
      ...
  }, {
    "ID": "0d76f613a0018aaf74eb8df7595fb889035332c9ed0fe6f31ce6f9a019f67fb2#0",
    "Type": "storjscan",
    "Wallet": "003bbdb149e6aa3a6e8f31b469379cea19559827",
    "Amount": {
      "value": "296.523254",
      "currency": "USDMicro"
    },
    "Received": {
      "value": "0",
      "currency": "USDMicro"
    },
    "Status": "confirmed",
    "Link": "https://etherscan.io/tx/0d76f613a0018aaf74eb8df7595fb889035332c9ed0fe6f31ce6f9a019f67fb2",
    "Timestamp": "2022-08-31T18:39:12Z"
  }
]
```

Note: replace the cookie in the above commands with a valid session cookie

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

### Tips
You can pretty print the JSOn responses by passing them to jq, i.e.
```bash
curl -sb GET http://localhost:10000/api/v0/payments/wallet/payments --header 'Cookie: <YOUR TOKEN KEY>' | jq '.'
```

you can save off variables such as the cookie or the wallet address for use in later commands, i.e.
```bash
COOKIE=$(storj-up credentials | grep -o 'Cookie.*')
ADDRESS=$(curl -sb GET http://localhost:10000/api/v0/payments/wallet --header "$COOKIE" | jq -r '.address')
```
then use them like
```bash
curl -sb GET http://localhost:10000/api/v0/payments/wallet --header "$COOKIE"
cethacea token transfer 1000 "$ADDRESS"
```

### Troubleshooting
If you get an error about not having enough tokens to deploy the contract, try exporting the CETH account and attempt to deploy again
```bash
export CETH_ACCOUNT=2e9a0761ce9815b95b2389634f6af66abe5fec2b1e04b772728442b4c35ea365
```

