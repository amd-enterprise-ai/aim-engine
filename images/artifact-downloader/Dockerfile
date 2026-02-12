FROM python:3.14-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    s3cmd kubectl \
    procps \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir -U huggingface_hub hf_transfer


COPY entrypoint.sh /entrypoint.sh
COPY progress-monitor.sh /progress-monitor.sh
COPY check-size.sh /check-size.sh
COPY hf-download.sh /hf-download.sh
COPY hf-verify.sh /hf-verify.sh
RUN mkdir -p /check-size /storage-initializer/scripts
COPY check-size/check-hf-size.py /check-size/check-hf-size.py
COPY kserve-entrypoint.sh /storage-initializer/scripts/initializer-entrypoint

RUN chmod +x /entrypoint.sh /check-size.sh /progress-monitor.sh /storage-initializer/scripts/initializer-entrypoint /hf-download.sh /hf-verify.sh


RUN mkdir /cache && chown 1000:1000 /cache

WORKDIR /cache
USER 1000

ENTRYPOINT ["/entrypoint.sh"]