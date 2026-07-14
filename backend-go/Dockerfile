FROM scratch
WORKDIR /app

COPY bin/creatorinsight-api /app/creatorinsight-api
COPY bin/creatorinsight-worker /app/creatorinsight-worker
COPY bin/noteinsight-simulator /app/noteinsight-simulator
COPY bin/noteinsight-corpusgen /app/noteinsight-corpusgen
COPY bin/noteinsight-seedgen /app/noteinsight-seedgen

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/app/creatorinsight-api"]
