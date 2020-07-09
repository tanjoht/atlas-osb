#!/usr/bin/env bash
export ATLAS_PUBLIC_KEY=$(jq '.broker.username' keys)
export ATLAS_PRIVATE_KEY=$(jq '.broker.password' keys)
./broker-tester $@

