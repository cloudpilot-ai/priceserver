#!/usr/bin/env bash

export PRICESERVER_IMAGE_REF=${PRICESERVER_IMAGE_REF:-public.ecr.aws/cloudpilotai/priceserver:latest}
export CLOUDPILOT_PRICE_HOST=pre-price.cloudpilot.ai

if [[ ${TARGET_CLUSTER} == "cloudpilot-production" ]]; then
  export GIN_MODE=release
  CLOUDPILOT_PRICE_HOST=price.cloudpilot.ai
fi

# Copy the files and apply envsubst
find config -type f -name '*.yaml' | while IFS= read -r file; do
    newfile="config-pro/${file#config/}"
    mkdir -p "$(dirname "$newfile")"
    envsubst < "$file" > "$newfile"
done
