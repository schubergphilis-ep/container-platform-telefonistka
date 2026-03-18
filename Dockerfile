FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o telefonistka .

FROM alpine:3.23.3 AS alpine-release
WORKDIR /srv
COPY templates/ /srv/templates/
COPY --from=builder /src/telefonistka /usr/local/bin/
USER 1001
ENTRYPOINT ["/usr/local/bin/telefonistka"]
CMD ["server"]

FROM scratch
WORKDIR /srv
COPY --from=alpine-release /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY templates/ /srv/templates/
COPY --from=builder /src/telefonistka /usr/local/bin/
USER 1001
ENTRYPOINT ["/usr/local/bin/telefonistka"]
CMD ["server"]
