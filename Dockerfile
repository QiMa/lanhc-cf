FROM alpine:3.20
WORKDIR /app
COPY ./bin/main ./main
COPY ./src ./src
EXPOSE 80
CMD ["./main"]
