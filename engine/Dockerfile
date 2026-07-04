FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/meridian ./cmd/meridian

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/meridian /meridian
EXPOSE 8080
VOLUME /data
ENTRYPOINT ["/meridian", "-addr", ":8080", "-data", "/data"]
