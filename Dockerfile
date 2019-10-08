FROM golang:1 AS build

WORKDIR /go/src/app
COPY . .

RUN go get -d -v ./...
RUN go install -v ./...

FROM debian:buster

RUN apt-get update -y \
 && apt-get upgrade -y \
 && apt-get install --no-install-recommends -y \
      iptables \
      strace \
      stress \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /go/bin/urkel /go/bin/urkel

CMD ["/go/bin/urkel", "serve"]
