#!/bin/bash

# Copyright 2022 Istio Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
function printlicense {
    FILE_PATH=$(dirname "$1")
    SOURCE=${FILE_PATH#"./licenses/"}
    if [ "${SOURCE}" == "github.com/hashicorp/go-version" ]; then
        echo "License for https://third-party-mirror.googlesource.com/go-version" >> THIRD_PARTY_NOTICE.txt
    elif [ "${SOURCE}" == "github.com/hashicorp/errwrap" ]; then
        echo "License for https://third-party-mirror.googlesource.com/golang/hashicorp/errwrap" >> THIRD_PARTY_NOTICE.txt
    elif [ "${SOURCE}" == "github.com/hashicorp/hcl" ]; then
        echo "License for https://third-party-mirror.googlesource.com/golang/hashicorp/hcl/" >> THIRD_PARTY_NOTICE.txt
    elif [ "${SOURCE}" == "github.com/hashicorp/go-multierror" ]; then
        echo "License for https://third-party-mirror.googlesource.com/golang/hashicorp/multierror" >> THIRD_PARTY_NOTICE.txt
    elif [ "${SOURCE}" == "github.com/hashicorp/golang-lru/v2" ]; then
        echo "License for https://third-party-mirror.googlesource.com/golang/lru" >> THIRD_PARTY_NOTICE.txt
    else
        echo "License for: ${SOURCE}" >> THIRD_PARTY_NOTICE.txt
    fi
    cat "$1" >> THIRD_PARTY_NOTICE.txt
    echo -en '\n' >> THIRD_PARTY_NOTICE.txt
}
touch THIRD_PARTY_NOTICE.txt
export -f printlicense
find . -name LICENSE -exec bash -c 'printlicense $@' bash {} \;