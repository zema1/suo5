#!/bin/bash

if [ -z "$2" ]; then
  echo "Usage: $0 service_url max_retries"
  exit 1
fi

service_url=$1
max_retries=$2

for _ in $(seq "$max_retries"); do
  if curl -s "$service_url" >/dev/null; then
    echo "Service is up and running!"
    exit 0
  fi
  echo "Service is not ready, sleep 1s"
  sleep 1
done

echo "Service could not be started within $max_retries seconds"
exit 1
