.PHONY: all
default: clean;

DOCKER_COMPOSE := docker-compose  -f infrastructure/docker-compose.yml --project-directory $(CURDIR)

# Together with the Dockerfile detect if in the container shell
ifndef PYTHON_IN_CONTAINER
    CMD_EXEC := ${DOCKER_COMPOSE} exec python
endif

build:
	${DOCKER_COMPOSE} build --no-cache

start:
	${DOCKER_COMPOSE} up -d

stop:
	${DOCKER_COMPOSE} down --remove-orphans

shell:
	${DOCKER_COMPOSE} exec python ash

install:
	${CMD_EXEC} pip3 install -r requirements.txt

clean:
	if [ -d "$(CURDIR)/venv" ]; then rm -Rf "$(CURDIR)/venv"; fi