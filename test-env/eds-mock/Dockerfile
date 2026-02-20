FROM python:3.12-slim

WORKDIR /app

COPY test-env/eds-mock/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY test-env/eds-mock/eds_mock.py /app/eds_mock.py

ENV PYTHONUNBUFFERED=1

ENTRYPOINT ["python", "/app/eds_mock.py"]
