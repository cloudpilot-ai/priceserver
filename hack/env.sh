#!/usr/bin/env bash

export GIT_VERSION=$(git describe --tags --always --dirty);
export GIT_COMMIT=$(git rev-parse HEAD);
export GIT_TREE_STATE=$(git status --porcelain 2>/dev/null | grep "^??" > /dev/null && echo "dirty" || echo "clean");
export BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ");
