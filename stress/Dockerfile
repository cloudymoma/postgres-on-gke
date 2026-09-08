FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /pgstress ./cmd/pgstress

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /pgstress /pgstress
USER nonroot:nonroot
ENTRYPOINT ["/pgstress"]
