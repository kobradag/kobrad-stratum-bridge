FROM golang:1.19.1 as builder

LABEL org.opencontainers.image.description="Dockerized Pyrin Stratum Bridge"
LABEL org.opencontainers.image.authors="onemorebsmith,kobrad"
LABEL org.opencontainers.image.source="https://github.com/kobradag/kobrad-stratum-bridge"

WORKDIR /go/src/app
ADD go.mod .
ADD go.sum .
RUN go mod download

ADD . .
RUN go build -o /go/bin/app ./cmd/kobradbridge


FROM gcr.io/distroless/base:nonroot
COPY --from=builder /go/bin/app /
COPY cmd/kobradbridge/config.yaml /

WORKDIR /
ENTRYPOINT ["/app"]
