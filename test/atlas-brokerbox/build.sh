GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build broker-tester.go
../../dev/scripts/build-production-binary.sh .
cp ../../mongodb-atlas-service-broker .
docker build -t jmimick/atlas-brokerbox .
rm mongodb-atlas-service-broker

