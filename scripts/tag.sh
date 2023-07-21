#!/bin/bash

versionNumber=${1}

if [ -z $versionNumber ]; then
    echo "Usage: $(basename $0) <a.b.c>"
    exit 1
fi

if [[ $versionNumber =~ ^[0-9]+\.[0-9]+ ]]; then
    tag="v${versionNumber}"
    git rev-list $tag > /dev/null 2>&1
    if [ $? -eq 0 ]; then
        echo "Tag ${tag} already exists"
        exit 1
    fi

    if [ ! -f charts/vals-operator/Chart.yaml ]; then
        echo "Please run from the git repo root directory"
        exit 1
    fi

    sed -i '' "s/version:.*/version: ${versionNumber}/g" charts/vals-operator/Chart.yaml
    sed -i '' "s/appVersion:.*/appVersion: ${tag}/g" charts/vals-operator/Chart.yaml

    branch=$(git rev-parse --abbrev-ref HEAD)
    git commit -S -am "Tagging version ${versionNumber}" && git push -u origin $branch && \
    git tag $tag && git push --tags -u origin $tag
fi
