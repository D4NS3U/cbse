FROM python:3.12-slim

ARG IMAGE_VERSION=dev
ARG VCS_REF=unknown

WORKDIR /app

COPY test-env/eds-mock/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY test-env/eds-mock/eds_mock.py /app/eds_mock.py

LABEL org.opencontainers.image.title="CBSE EDS Mock" \
      org.opencontainers.image.version="${IMAGE_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.source="https://github.com/d4ns3u/cbse"

ENV PYTHONUNBUFFERED=1

ENTRYPOINT ["python", "/app/eds_mock.py"]
