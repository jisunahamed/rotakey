-- Audio models previously inherited chat flags and failed chat capability checks.
-- Restore only documented model IDs; leave operator enable/disable choices intact.
UPDATE model_routes
SET supports_chat=FALSE, supports_responses=FALSE, supports_messages=FALSE,
    supports_embeddings=FALSE,
    capability_status=CASE WHEN capability_status='failed' THEN 'catalog_verified' ELSE capability_status END,
    capability_error='',
    capability_profile=capability_profile || jsonb_build_object(
        'model_type', CASE WHEN upstream_model LIKE 'canopylabs/orpheus-%' THEN 'audio_speech' ELSE 'audio_transcription' END,
        'chat','off','responses','off','messages','off','embeddings','off',
        'tools','off','thinking','off','streaming','off'
    ) || CASE
        WHEN upstream_model LIKE 'canopylabs/orpheus-%' THEN '{"audio/speech":"native"}'::jsonb
        WHEN upstream_model='whisper-large-v3-turbo' THEN '{"audio/transcriptions":"native"}'::jsonb
        ELSE '{"audio/transcriptions":"native","audio/translations":"native"}'::jsonb
    END,
    updated_at=NOW()
WHERE upstream_model IN ('whisper-large-v3','whisper-large-v3-turbo','whisper-1',
    'canopylabs/orpheus-v1-english','canopylabs/orpheus-arabic-saudi');
