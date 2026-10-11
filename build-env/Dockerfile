FROM --platform=linux/amd64 golang:1.27.2-trixie@sha256:e58d6f83b3416618d8bcac2b3dde1b7f7e3c4a77d25e88637f8bbae81536c48d

ARG DEBIAN_FRONTEND=noninteractive

RUN rm -f /etc/apt/sources.list.d/* \
    && printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20261010T000000Z/ trixie main\n' > /etc/apt/sources.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        make \
        gcc \
        g++ \
        autoconf \
        automake \
        libtool \
        pkg-config \
        python3 \
        python3-venv \
        perl \
        patch \
        xz-utils \
        git \
        nodejs \
        gcc-14-aarch64-linux-gnu=14.2.0-19cross1 \
        binutils-aarch64-linux-gnu=2.44-3 \
        linux-libc-dev-arm64-cross=6.12.38-1cross1 \
        libc6-dev-arm64-cross=2.41-11cross1 \
    && ln -s /usr/bin/aarch64-linux-gnu-gcc-14 /usr/local/bin/aarch64-linux-gnu-gcc \
    && test "$(aarch64-linux-gnu-gcc -dumpfullversion)" = "14.2.0" \
    && rm -rf /var/lib/apt/lists/*

COPY build-env/requirements.txt /tmp/ocfusion-build-requirements.txt

RUN python3 -m venv /opt/ocfusion-build \
    && /opt/ocfusion-build/bin/pip install \
        --require-hashes \
        --only-binary=:all: \
        -r /tmp/ocfusion-build-requirements.txt \
    && rm /tmp/ocfusion-build-requirements.txt

ENV PATH="/opt/ocfusion-build/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin" \
    GOTOOLCHAIN=local

WORKDIR /work
