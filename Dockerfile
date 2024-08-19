FROM golang

WORKDIR /app

COPY . ./
RUN go mod download
RUN go build cmd/omni/main.go

CMD ["/app/main"]
