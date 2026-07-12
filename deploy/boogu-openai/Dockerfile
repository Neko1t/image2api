FROM nvidia/cuda:12.6.3-cudnn-devel-ubuntu22.04

ENV DEBIAN_FRONTEND=noninteractive

ARG PYTHON_VERSION=3.11.13

RUN apt-get update && apt-get install -y \
    build-essential \
    ca-certificates \
    curl \
    git \
    libbz2-dev \
    libffi-dev \
    liblzma-dev \
    libncursesw5-dev \
    libreadline-dev \
    libsqlite3-dev \
    libssl-dev \
    tk-dev \
    uuid-dev \
    wget \
    xz-utils \
    zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSLO "https://www.python.org/ftp/python/${PYTHON_VERSION}/Python-${PYTHON_VERSION}.tgz" \
    && tar -xzf "Python-${PYTHON_VERSION}.tgz" \
    && cd "Python-${PYTHON_VERSION}" \
    && ./configure --enable-shared --with-ensurepip=install \
    && make -j"$(nproc)" \
    && make altinstall \
    && cd / \
    && rm -rf "Python-${PYTHON_VERSION}" "Python-${PYTHON_VERSION}.tgz" \
    && ldconfig

RUN ln -sf /usr/local/bin/python3.11 /usr/bin/python3.11 \
    && ln -sf /usr/local/bin/python3.11 /usr/bin/python3 \
    && ln -sf /usr/local/bin/python3.11 /usr/bin/python \
    && python3.11 -m pip install --upgrade pip setuptools wheel

ARG BOOGU_GIT_REF=df9a219fccd8954df7cf16e71453c19f7a72dbba
RUN git clone https://github.com/boogu-project/Boogu-Image.git /opt/Boogu-Image \
    && cd /opt/Boogu-Image \
    && git checkout ${BOOGU_GIT_REF}

ARG BOOGU_TORCH_REQUIREMENTS=torch2.7-cu126.txt
RUN python3.11 -m pip install -r "/opt/Boogu-Image/requirements/${BOOGU_TORCH_REQUIREMENTS}"
RUN python3.11 -m pip install -e /opt/Boogu-Image

WORKDIR /app
COPY requirements.txt /app/
RUN python3.11 -m pip install -r /app/requirements.txt

ARG BOOGU_INSTALL_FLASH_ATTN=true
ARG FLASH_ATTN_VERSION=2.8.3
ARG TORCH_CUDA_ARCH_LIST=8.9
ARG MAX_JOBS=1
ENV TORCH_CUDA_ARCH_LIST=${TORCH_CUDA_ARCH_LIST} \
    MAX_JOBS=${MAX_JOBS}
RUN case "$BOOGU_INSTALL_FLASH_ATTN" in \
        true|helper) \
            python3.11 /opt/Boogu-Image/utils/get_flash_attn.py \
            || python3.11 -m pip install "flash-attn==${FLASH_ATTN_VERSION}" --no-build-isolation; \
            ;; \
        source) \
            python3.11 -m pip install ninja packaging \
            && python3.11 -m pip install -v "flash-attn==${FLASH_ATTN_VERSION}" --no-build-isolation --no-binary=flash-attn; \
            ;; \
        false|0|no) \
            echo "Skipping flash-attn install; Boogu will use fallback attention/SwiGLU paths."; \
            ;; \
        *) \
            echo "Invalid BOOGU_INSTALL_FLASH_ATTN=${BOOGU_INSTALL_FLASH_ATTN}. Use true, helper, source, or false." >&2; \
            exit 1; \
            ;; \
    esac

ENV HF_HOME=/cache/hf

COPY app.py /app/

EXPOSE 8008

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8008", "--log-level", "info"]
