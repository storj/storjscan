# storjscan
`STORJ` token payments service: `storjscan` scan manages wallet addresses and scans the blockchain for new STORJ token transfer transactions for the managed wallet addresses. It also calculates the USD amount of the transfers based on actual price.

## Build
```bash
go build -o build/ ./cmd/storjscan
```

## Test
Storjscan creates local POS blockchain network to test code that interacts with Ethereum.
Tests that require postgres DB are skipped by default. 
To enable it `STORJ_POSTGRES_TEST` env should be set with postgres connection string pointing to test database.
```bash
export STORJ_POSTGRES_TEST=postgres://postgres@localhost/teststorjscan?sslmode=disable
go test -race ./...
```

## Run
Running `storjscan` configured to main net Ethereum node and `STORJ` token.
```bash
./build/storjscan run \
--database=postgres://postgres@localhost/storjscandb?sslmode=disable \
--tokens.endpoint="https://mainnet.example-node.address" \
--tokens.token-address=0xB64ef51C888972c908CFacf59B47C1AfBC0Ab8aC \
--api.address="127.0.0.1:10000" \
--api.keys="gJFzBF7EQEK7RxATlvNiOg==" 
```

### Payments
Get payments of random Ethereum address `0x69A0a76DaB9CE2bB2BDb3ba129eEd79606b4C2C6`
```bash
curl -X GET -H "STORJSCAN_API_KEY: gJFzBF7EQEK7RxATlvNiOg==" \
http://127.0.0.1:10000/api/v0/tokens/payments/0x69A0a76DaB9CE2bB2BDb3ba129eEd79606b4C2C6
```
Output
```json
[
  {
    "From":"0xdfd5293d8e347dfe59e90efd55b2956a1343963d",
    "TokenValue":45246900000,
    "Transaction":"0x451b6ef600db4f059d2b792b524c7de5eee837631266f8cdc53997098723f438"
  }
]
```

## Contracts
Storjscan uses `abigen` tool to generate `ERC20` interface and `TestToken` contract go bindings.
To generate contract go bindings `contracts/TestToken.sol` should be compiled first.

### TestToken
`TestToken` is test `ERC20` token implementation which is deployed to a local test network and used for testing purposes.
Source code of `TestToken` is located at `contract/TestToken.sol`. 
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
