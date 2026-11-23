FROM golang:1.26

RUN apt-get update \
 && apt-get install -y --no-install-recommends fuse3 \
 && apt-get clean
