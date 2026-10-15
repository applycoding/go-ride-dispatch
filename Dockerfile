FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dispatch ./cmd/dispatch

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -H -u 65532 app
COPY --from=build /out/dispatch /usr/local/bin/dispatch
USER app
EXPOSE 8080
ENTRYPOINT ["dispatch"]
