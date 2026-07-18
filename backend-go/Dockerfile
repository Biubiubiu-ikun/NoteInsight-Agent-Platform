FROM scratch
WORKDIR /app

COPY bin/creatorinsight-api /app/creatorinsight-api
COPY bin/creatorinsight-worker /app/creatorinsight-worker
COPY bin/noteinsight-simulator /app/noteinsight-simulator
COPY bin/noteinsight-corpusgen /app/noteinsight-corpusgen
COPY bin/noteinsight-seedgen /app/noteinsight-seedgen
COPY bin/creatorinsight-migrate /app/creatorinsight-migrate
COPY bin/noteinsight-reconcile /app/noteinsight-reconcile
COPY bin/noteinsight-materialize /app/noteinsight-materialize
COPY bin/noteinsight-maintenance /app/noteinsight-maintenance
COPY bin/noteinsight-dlqctl /app/noteinsight-dlqctl
COPY bin/noteinsight-evalfreeze /app/noteinsight-evalfreeze
COPY bin/noteinsight-datasetfreeze /app/noteinsight-datasetfreeze
COPY bin/noteinsight-evidence /app/noteinsight-evidence
COPY bin/noteinsight-retrieval-index /app/noteinsight-retrieval-index
COPY bin/noteinsight-vector-index /app/noteinsight-vector-index
COPY bin/noteinsight-retrieval-search /app/noteinsight-retrieval-search
COPY bin/noteinsight-retrieval-eval /app/noteinsight-retrieval-eval
COPY bin/noteinsight-eval-dataset /app/noteinsight-eval-dataset
COPY bin/noteinsight-benchmark-audit /app/noteinsight-benchmark-audit
COPY migrations /app/migrations

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/app/creatorinsight-api"]
