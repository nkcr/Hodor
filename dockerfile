# Ensure "hodor-linux-amd64" is present and provide the "config.json". Then: 
#   docker build -t hodor .
#   docker run -p 3333:3333 -v $(pwd)/data:/data -v $(pwd)/config.json:/config.json hodor
FROM alpine:3.14
COPY --chmod=0755 ./hodor-linux-amd64 /hodor
ENTRYPOINT ["/hodor", "--dbfilepath", "/data/hodor.db", "--config", \
  "/config.json", "--listen", "0.0.0.0:3333"]