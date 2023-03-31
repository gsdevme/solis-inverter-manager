FROM python:3-alpine as base

LABEL MAINTAINER="Gavin Staniforth"

ENV PYTHON_IN_CONTAINER=1

RUN mkdir /app
WORKDIR /app

COPY . .

RUN pip install --upgrade pip && \
    apk add make

FROM base as build

RUN pip3 install pylint

FROM build as dev

CMD ["tail", "-f", "/dev/null"]

FROM base as prod

RUN pip3 install -r requirements.txt

ENTRYPOINT [ "python", "./main.py" ]
CMD [ "publish-mqtt" ]
