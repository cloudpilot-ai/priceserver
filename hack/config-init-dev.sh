#!/usr/bin/env bash

export PRICESERVER_IMAGE_REF=ko://github.com/cloudpilot-ai/priceserver/cmd
export CLOUDPILOT_PRICE_HOST=pre-price.cloudpilot.ai

# Copy the files and apply envsubst
find config -type f -name '*.yaml' | while IFS= read -r file; do
    newfile="config-dev/${file#config/}"
    mkdir -p "$(dirname "$newfile")"
    envsubst < "$file" > "$newfile"
done
