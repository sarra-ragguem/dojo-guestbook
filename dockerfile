FROM golang:1.25
WORKDIR /dojo
COPY go.mod go.sum ./
RUN go mod download
COPY ./public public
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o ./binary ./main.go

FROM alpine
WORKDIR /dojo
COPY --from=0 /dojo/binary ./binary
COPY ./public public
CMD ["./binary"]
