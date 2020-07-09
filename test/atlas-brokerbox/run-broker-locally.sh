#!/usr/bin/env bash

#
# NOTE - only run this if you want to run the broker directly on
# your own system. This file is used by the Docker container
# to run the broker in that runtime.

export BROKER_LOG_LEVEL=DEBUG
export BROKER_HOST=0.0.0.0
export BROKER_PORT=4000
export BROKER_APIKEYS=$(cat ./keys)
export ATLAS_BROKER_TEMPLATEDIR=$(pwd)/plans
env
ls -l plans
env | grep BROKER

./mongodb-atlas-service-broker

