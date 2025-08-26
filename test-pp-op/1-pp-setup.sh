set -e
set -x

if ! [ -f "example.env" ]; then
  echo "example.env not found. You may need to run 'git checkout example.env'"
  exit 1
fi
cp example.env .env

source .env

if [ "$CHECK_TYPE" == "mainnet" ]; then
  make mainnet
  TMPSTR=""
  while [ -z "$TMPSTR" ]; do
    echo "Waiting for mainnet to be ready..."
    sleep 1
    docker logs xlayer-seq > tmp.log 2>&1
    TMPSTR=$(grep "Waiting for txs from the pool" tmp.log || true)
  done
  rm -f tmp.log
else
  make run
fi
