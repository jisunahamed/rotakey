ALTER TABLE providers ADD COLUMN inference_endpoint TEXT NOT NULL DEFAULT 'auto'
    CHECK (inference_endpoint IN ('auto','chat','responses'));
