FROM golang:1 AS build

WORKDIR /go/src/app
COPY . .

RUN go get -d -v ./...
RUN go install -v ./...

FROM debian:buster

COPY --from=build /go/bin/urkel /go/bin/urkel

CMD ["/go/bin/urkel", "serve"]
