#!/usr/bin/bash
cd "$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null && pwd )"

set -ex

if [ ! "$(which storjscan )" ]; then
   go install storj.io/storjscan@latest
fi

if [ ! "$(which cethacea)" ]; then
   go install github.com/elek/cethacea@latest
fi

cd ..

docker-compose up -d

# todo: find a better way than sleep to wait until geth is ready
sleep 5

export CETH_CHAIN=http://localhost:8545

cethacea contract deploy --name TOKEN contracts/build/TestToken.bin --abi contracts/build/TestToken.abi '(uint256)' 1000000000000
cethacea token balance --account=key1
cethacea token transfer 1000000000 key2
cethacea token balance --account=key2

curl -X GET -u "eu1:eu1secret" http://127.0.0.1:12000/api/v0/auth/whoami
curl -X GET -u "us1:us1secret" http://127.0.0.1:12000/api/v0/auth/whoami

storjscan mnemonic > .mnemonic
storjscan generate --api-key us1 --api-secret us1secret --address http://127.0.0.1:12000
storjscan mnemonic > .mnemonic
storjscan generate --api-key eu1 --api-secret eu1secret --address http://127.0.0.1:12000

curl -X POST -u "eu1:eu1secret" http://127.0.0.1:12000/api/v0/wallets/claim
curl -X POST -u "us1:us1secret" http://127.0.0.1:12000/api/v0/wallets/claim

export WALLET=$(grep -w -o "0x[0-9a-zA-Z]*" .accounts.yaml | sed -n 1p)

curl -X GET -u "eu1:eu1secret" http://127.0.0.1:12000/api/v0/tokens/payments/$WALLET

export WALLET=$(grep -w -o "0x[0-9a-zA-Z]*" .accounts.yaml | sed -n 2p)

curl -X GET -u "eu1:eu1secret" http://127.0.0.1:12000/api/v0/tokens/payments/$WALLET

# todo: find a way to verify these outputs

rm -rf .mnemonic

docker compose down
