# Ensure "hodor-linux-amd64" is present and provide the "config.json". Then: 
#   docker build -t hodor .
#   docker run -p 3333:3333 -v $(pwd)/data:/data -v $(pwd)/config.json:/config.json hodor
FROM alpine:3.14
ENV CONFIG_PATH "/config.json"
COPY --chmod=0755 ./hodor-linux-amd64 /hodor
# Not using the shell form to catch the SIGINT signal, but using a subcommand to
# allow for shell processing of the CONFIG_PATH variable.
ENTRYPOINT ["/bin/sh", "-c", "/hodor --dbfilepath /data/hodor.db --config ${CONFIG_PATH} --listen 0.0.0.0:3333"]