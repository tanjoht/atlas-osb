#!/usr/bin/env bash
APIKEYS_FILE=${1:-$(pwd)/demo-keys}
PLANS_FOLDER=${2:-$(pwd)/plans}
PORT=4000
docker run -it -p ${PORT}:${PORT} -v ${APIKEYS_FILE}:/keys -v ${PLANS_FOLDER}:/plans jmimick/brokerbox
