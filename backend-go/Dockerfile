FROM scratch
WORKDIR /app

COPY bin/creatorinsight-api /app/creatorinsight-api
COPY bin/creatorinsight-worker /app/creatorinsight-worker
COPY bin/noteinsight-simulator /app/noteinsight-simulator

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/app/creatorinsight-api"]
