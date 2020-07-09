FROM alpine 
RUN apk update
RUN apk upgrade
RUN apk add bash

WORKDIR /
COPY mongodb-atlas-service-broker .
COPY broker-tester .
COPY plans plans
COPY my-creds.json keys
COPY run-broker-locally.sh run-broker-locally.sh

CMD ["./run-broker-locally.sh"]
