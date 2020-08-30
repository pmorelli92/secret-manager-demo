FROM golang:1.15-alpine3.12 AS compiler

WORKDIR /builder
ADD . ./
RUN CGO_ENABLED=0 go build main.go

FROM scratch
COPY --from=compiler /builder/main /main
ENTRYPOINT ["/main"]
