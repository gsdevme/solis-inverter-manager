FROM python:3-alpine as base

LABEL MAINTAINER="Gavin Staniforth"

ENV PYTHON_IN_CONTAINER=1

RUN mkdir /app
WORKDIR /app

COPY .env main.py requirements.txt /app

RUN pip install --upgrade pip && \
    apk add make

from base as dev

CMD ["tail", "-f", "/dev/null"]

from base as prod

RUN pip3 install -r requirements.txt

#CMD [ "python", "./main.py" ]