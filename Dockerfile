FROM golang:1.23 AS build
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -ldflags="-s -w" -o /app .

FROM gcr.io/distroless/static
COPY --from=build /app /app
EXPOSE 8000
CMD ["/app"]
