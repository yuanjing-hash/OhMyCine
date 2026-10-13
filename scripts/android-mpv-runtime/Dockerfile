FROM ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install --no-install-recommends -y \
    autoconf automake build-essential ca-certificates cmake curl git gperf \
    libtool ninja-build pkg-config python3 python3-pip python3-venv unzip xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/runtime-tools \
    && /opt/runtime-tools/bin/pip install --no-cache-dir meson==1.8.3 mako==1.3.10 jinja2==3.1.6
ENV PATH="/opt/runtime-tools/bin:${PATH}"
WORKDIR /work
ENTRYPOINT ["python3", "/builder/build-runtime.py"]
