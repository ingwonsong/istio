CLOUDESF_VERSION = "cloudesf_20210816_00_RC00"

SHELL := /bin/bash -o pipefail

# TODO(taoxuy): consider removing this target/script as later we use copybara to automatically
# sync the config.
.PHONY:cloudesf.format
cloudesf.format:
	./tests/integration/cloudesf/configs/format_configs.sh
