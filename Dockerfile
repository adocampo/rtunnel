FROM scratch
COPY bin/rtunnel-linux-amd64 /rtunnel
ENTRYPOINT ["/rtunnel"]
CMD ["client"]
