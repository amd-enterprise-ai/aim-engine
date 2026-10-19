# syntax=docker/dockerfile:1
# cloudpathlib 0.20.0 uses pathlib internals removed in 3.14, so pin 3.12.
FROM python:3.12-slim

# Optional build secret id=pip_ca: a CA bundle used only to let pip validate TLS
# behind an intercepting proxy. Never copied into the image; absent on clean
# networks, where pip's default bundle is used.
RUN --mount=type=secret,id=pip_ca \
    apt-get update && apt-get install -y --no-install-recommends \
    kubectl \
    procps \
    && rm -rf /var/lib/apt/lists/* \
    && if [ -f /run/secrets/pip_ca ]; then export PIP_CERT=/run/secrets/pip_ca; fi \
    # hf CLI imports click directly but typer no longer pulls it in.
    && pip install --no-cache-dir -U huggingface_hub==1.11.0 hf_transfer hf_xet click \
    # Pin boto3/botocore (not just transitively) for reproducible signing.
    && pip install --no-cache-dir \
        "cloudpathlib[s3]==0.20.0" \
        "boto3==1.35.99" \
        "botocore==1.35.99" \
        "tenacity==9.0.0"

ENV PYTHONUNBUFFERED=1
# Make the s3_downloader package importable via `python -m s3_downloader`.
ENV PYTHONPATH=/opt

COPY entrypoint.sh /entrypoint.sh
COPY adapter-stage.sh /adapter-stage.sh
COPY adapter-reap.sh /adapter-reap.sh
COPY adapter-subtree-sync.sh /adapter-subtree-sync.sh
COPY progress-monitor.sh /progress-monitor.sh
COPY check-size.sh /check-size.sh
COPY combine-ca.sh /combine-ca.sh
COPY hf-download.sh /hf-download.sh
COPY hf-verify.sh /hf-verify.sh
RUN mkdir -p /check-size /storage-initializer/scripts
COPY check-size/check-hf-size.py /check-size/check-hf-size.py
COPY verify-files.py /verify-files.py
COPY kserve-entrypoint.sh /storage-initializer/scripts/initializer-entrypoint
COPY s3_downloader /opt/s3_downloader

RUN chmod +x /entrypoint.sh /adapter-stage.sh /adapter-reap.sh /adapter-subtree-sync.sh /check-size.sh /combine-ca.sh /progress-monitor.sh /storage-initializer/scripts/initializer-entrypoint /hf-download.sh /hf-verify.sh


RUN mkdir -p /cache/models && chown -R 1000:1000 /cache

WORKDIR /cache/models
USER 1000

ENTRYPOINT ["/entrypoint.sh"]