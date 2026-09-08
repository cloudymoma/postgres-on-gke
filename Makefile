# Topology presets, in the spirit of elastic-on-gke's `make init_*`.
# Configure project/region in config.sh (or via environment) first.

.PHONY: init_demo init_prod status clean_demo clean_prod

# Single-instance Postgres on a cheap zonal cluster (PoC).
init_demo:
	./bin/demo.sh up

# 3-instance HA Postgres across 3 zones with GCS backups and internal LBs.
init_prod:
	./bin/gke.sh create
	./bin/cnpg.sh install
	./bin/gcs_backup.sh setup
	./bin/pg.sh deploy prod
	./bin/lb.sh deploy

status:
	./bin/pg.sh status

clean_demo:
	./bin/gke.sh delete demo

clean_prod:
	./bin/gke.sh delete
