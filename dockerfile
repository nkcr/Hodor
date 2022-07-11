# Ensure "hodor-linux-amd64" and "data/config.json" are present. Then: 
#   docker build -t hodor .
#   docker run -p 3333:3333 -v $(pwd)/data:/data hodor
FROM alpine:3.14
COPY --chmod=0755 ./hodor-linux-amd64 /hodor
COPY --chmod=0755 config.json /data/config.json
ENTRYPOINT ["/hodor", "--dbfilepath", "/data/hodor.db", "--config", \
  "/data/config.json", "--listen", "0.0.0.0:3333"]