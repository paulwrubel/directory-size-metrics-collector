FROM scratch

ADD collector /app/collector

CMD [ "/app/collector", "/app/config.yaml" ]