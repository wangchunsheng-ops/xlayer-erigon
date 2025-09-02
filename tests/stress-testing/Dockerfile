FROM ubuntu:22.04 AS builder

RUN apt-get update && apt-get install -y \
    build-essential \
    libssl-dev \
    git \
    zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

#https://github.com/giltene/wrk2.git
#https://github.com/kinvolk/wrk2.git
#https://github.com/AmpereTravis/wrk2-aarch64
RUN git clone https://github.com/AmpereTravis/wrk2-aarch64 && \
    cd wrk2-aarch64 && \
    make

FROM ubuntu:22.04

RUN apt-get update && apt-get install -y \
    libssl3 \
    netbase \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/wrk2-aarch64/wrk /usr/local/bin/wrk2

WORKDIR /scripts

ENTRYPOINT ["/bin/sh"]