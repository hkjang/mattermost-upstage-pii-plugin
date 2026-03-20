import React, {useEffect, useMemo, useRef, useState} from 'react';

import type {WebSocketMessage} from '@mattermost/client';

import PostText from './post_text';

import {getPostDebug} from '../client';
import {isUpstageAwaitingFirstChunk} from '../streaming';

type PostUpdateData = {
    post_id?: string;
    next?: string;
    control?: string;
};

type Props = {
    post: any;
    websocketRegister: (postID: string, listenerID: string, listener: (msg: WebSocketMessage<PostUpdateData>) => void) => void;
    websocketUnregister: (postID: string, listenerID: string) => void;
};

const containerStyle: React.CSSProperties = {
    display: 'flex',
    flexDirection: 'column',
    gap: '8px',
};

const statusStyle: React.CSSProperties = {
    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
    fontSize: '12px',
    fontWeight: 600,
    letterSpacing: '0.01em',
};

const precontentStyle: React.CSSProperties = {
    alignItems: 'center',
    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
    display: 'inline-flex',
    fontSize: '13px',
    gap: '8px',
};

const spinnerStyle: React.CSSProperties = {
    animation: 'upstage-stream-cursor-blink 700ms linear infinite',
    background: 'rgba(var(--center-channel-color-rgb), 0.16)',
    borderRadius: '999px',
    display: 'inline-block',
    height: '10px',
    width: '10px',
};

const toolbarStyle: React.CSSProperties = {
    alignItems: 'center',
    display: 'flex',
    flexWrap: 'wrap',
    gap: '8px',
};

const buttonStyle: React.CSSProperties = {
    background: 'rgba(var(--button-bg-rgb), 0.12)',
    border: '1px solid rgba(var(--button-bg-rgb), 0.28)',
    borderRadius: '999px',
    color: 'rgb(var(--button-bg-rgb))',
    cursor: 'pointer',
    fontSize: '12px',
    fontWeight: 600,
    padding: '6px 12px',
};

const modalBackdropStyle: React.CSSProperties = {
    alignItems: 'center',
    background: 'rgba(0, 0, 0, 0.44)',
    bottom: 0,
    display: 'flex',
    justifyContent: 'center',
    left: 0,
    padding: '24px',
    position: 'fixed',
    right: 0,
    top: 0,
    zIndex: 2147483000,
};

const modalCardStyle: React.CSSProperties = {
    background: 'var(--center-channel-bg)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '12px',
    boxShadow: '0 16px 48px rgba(0, 0, 0, 0.24)',
    color: 'rgb(var(--center-channel-color-rgb))',
    display: 'flex',
    flexDirection: 'column',
    maxHeight: 'calc(100vh - 48px)',
    overflow: 'hidden',
    width: 'min(960px, calc(100vw - 32px))',
};

const modalHeaderStyle: React.CSSProperties = {
    alignItems: 'center',
    borderBottom: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    display: 'flex',
    gap: '12px',
    justifyContent: 'space-between',
    padding: '16px 20px',
};

const modalBodyStyle: React.CSSProperties = {
    display: 'flex',
    flexDirection: 'column',
    gap: '16px',
    overflowY: 'auto',
    padding: '20px',
};

const debugPanelStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.08)',
    borderRadius: '10px',
    display: 'flex',
    flexDirection: 'column',
    gap: '12px',
    minHeight: 0,
    padding: '16px',
};

const debugPreStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    borderRadius: '8px',
    fontSize: '12px',
    margin: 0,
    maxHeight: '52vh',
    minHeight: '160px',
    overflow: 'auto',
    padding: '12px',
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
};

const tabRowStyle: React.CSSProperties = {
    display: 'flex',
    flexWrap: 'wrap',
    gap: '8px',
};

type DebugSection = 'request' | 'response';

export default function UpstageBotPost(props: Props) {
    const [message, setMessage] = useState(getRenderableMessage(props.post));
    const [generating, setGenerating] = useState(isStreamingPost(props.post));
    const [precontent, setPrecontent] = useState(isUpstageAwaitingFirstChunk(props.post));
    const [showDebugModal, setShowDebugModal] = useState(false);
    const [activeDebugSection, setActiveDebugSection] = useState<DebugSection>('request');
    const [remoteRequestDebug, setRemoteRequestDebug] = useState('');
    const [remoteResponseDebug, setRemoteResponseDebug] = useState('');
    const [debugLoading, setDebugLoading] = useState(false);
    const [debugError, setDebugError] = useState('');
    const listenerID = useRef(`upstage-${Math.random().toString(36).slice(2)}`);
    const localInputDebug = normalizeDebugPayload(props.post?.props?.upstage_request_input || props.post?.props?.upstage_error_input);
    const localOutputDebug = normalizeDebugPayload(props.post?.props?.upstage_response_output || props.post?.props?.upstage_error_output);
    const inputDebug = firstDefinedDebug(remoteRequestDebug, localInputDebug);
    const outputDebug = firstDefinedDebug(remoteResponseDebug, localOutputDebug);
    const hasRemoteDebugSource = Boolean(props.post?.id && props.post?.props?.upstage_correlation_id);
    const hasInputDebug = hasDebugFlag(props.post?.props?.upstage_has_request_debug) || localInputDebug !== '' || hasRemoteDebugSource;
    const hasOutputDebug = hasDebugFlag(props.post?.props?.upstage_has_response_debug) || localOutputDebug !== '' || hasRemoteDebugSource;
    const canShowDebug = hasInputDebug || hasOutputDebug;
    const debugModalTitle = activeDebugSection === 'response' ? 'PII API 응답 파라미터' : 'PII API 요청 파라미터';

    useEffect(() => {
        setMessage(getRenderableMessage(props.post));
        setGenerating(isStreamingPost(props.post));
        setPrecontent(isUpstageAwaitingFirstChunk(props.post));
        setShowDebugModal(false);
        setRemoteRequestDebug('');
        setRemoteResponseDebug('');
        setDebugLoading(false);
        setDebugError('');
        setActiveDebugSection((props.post?.props?.upstage_has_response_debug || props.post?.props?.upstage_response_output) ? 'response' : 'request');
    }, [
        props.post.id,
        props.post.message,
        props.post.props?.upstage_streaming,
        props.post.props?.upstage_stream_status,
        props.post.props?.upstage_stream_placeholder,
        props.post.props?.upstage_request_input,
        props.post.props?.upstage_response_output,
        props.post.props?.upstage_error_input,
        props.post.props?.upstage_error_output,
        props.post.props?.upstage_has_request_debug,
        props.post.props?.upstage_has_response_debug,
    ]);

    useEffect(() => {
        if (!showDebugModal) {
            return undefined;
        }

        const onKeyDown = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                setShowDebugModal(false);
            }
        };

        document.addEventListener('keydown', onKeyDown);
        return () => document.removeEventListener('keydown', onKeyDown);
    }, [showDebugModal]);

    useEffect(() => {
        if (!showDebugModal || !hasRemoteDebugSource || debugLoading) {
            return undefined;
        }
        if (remoteRequestDebug !== '' || remoteResponseDebug !== '') {
            return undefined;
        }

        let cancelled = false;
        setDebugLoading(true);
        setDebugError('');

        getPostDebug(props.post.id).then((payload) => {
            if (cancelled) {
                return;
            }
            setRemoteRequestDebug(normalizeDebugPayload(payload.request));
            setRemoteResponseDebug(normalizeDebugPayload(payload.response));
        }).catch((error: Error) => {
            if (cancelled) {
                return;
            }
            setDebugError(error.message || '디버그 payload를 불러오지 못했습니다.');
        }).finally(() => {
            if (!cancelled) {
                setDebugLoading(false);
            }
        });

        return () => {
            cancelled = true;
        };
    }, [debugLoading, hasRemoteDebugSource, props.post.id, remoteRequestDebug, remoteResponseDebug, showDebugModal]);

    const listener = useMemo(() => {
        return (msg: WebSocketMessage<PostUpdateData>) => {
            const data = msg?.data || {};
            if (data.post_id !== props.post.id) {
                return;
            }

            if (data.control === 'start') {
                setGenerating(true);
                setPrecontent(true);
                setMessage('');
                return;
            }

            if (typeof data.next === 'string' && data.next !== '') {
                setGenerating(true);
                setPrecontent(false);
                setMessage(data.next);
                return;
            }

            if (data.control === 'end' || data.control === 'cancel') {
                setGenerating(false);
                setPrecontent(false);
            }
        };
    }, [props.post.id]);

    useEffect(() => {
        props.websocketRegister(props.post.id, listenerID.current, listener);
        return () => {
            props.websocketUnregister(props.post.id, listenerID.current);
        };
    }, [listener, props.post.id, props.websocketRegister, props.websocketUnregister]);

    return (
        <div
            data-testid='upstage-bot-post'
            style={containerStyle}
        >
            {canShowDebug && (
                <div style={toolbarStyle}>
                    {hasInputDebug && (
                        <button
                            style={buttonStyle}
                            type='button'
                            onClick={() => {
                                setActiveDebugSection('request');
                                setShowDebugModal(true);
                            }}
                        >
                            {'PII API 요청 파라미터 보기'}
                        </button>
                    )}
                    {hasOutputDebug && (
                        <button
                            style={buttonStyle}
                            type='button'
                            onClick={() => {
                                setActiveDebugSection('response');
                                setShowDebugModal(true);
                            }}
                        >
                            {'PII API 응답 파라미터 보기'}
                        </button>
                    )}
                </div>
            )}
            {precontent && (
                <span style={precontentStyle}>
                    <span style={spinnerStyle}/>
                    {'PII 추출 시작 중...'}
                </span>
            )}
            <PostText
                channelID={props.post.channel_id}
                message={message}
                postID={props.post.id}
                showCursor={generating && !precontent}
            />
            {generating && !precontent && (
                <span style={statusStyle}>
                    {'PII 추출 중...'}
                </span>
            )}
            {showDebugModal && canShowDebug && (
                <div
                    aria-modal='true'
                    role='dialog'
                    style={modalBackdropStyle}
                    onClick={() => setShowDebugModal(false)}
                >
                    <div
                        style={modalCardStyle}
                        onClick={(event) => event.stopPropagation()}
                    >
                        <div style={modalHeaderStyle}>
                            <div style={{display: 'flex', flexDirection: 'column', gap: '4px'}}>
                                <strong>{debugModalTitle}</strong>
                                <span style={statusStyle}>
                                    {`Correlation ID: ${props.post?.props?.upstage_correlation_id || '-'}`}
                                </span>
                            </div>
                            <button
                                style={buttonStyle}
                                type='button'
                                onClick={() => setShowDebugModal(false)}
                            >
                                {'닫기'}
                            </button>
                        </div>
                        <div style={modalBodyStyle}>
                            {debugLoading && (
                                <span style={statusStyle}>
                                    {'PII 디버그 payload를 불러오는 중...'}
                                </span>
                            )}
                            {debugError !== '' && (
                                <section style={debugPanelStyle}>
                                    <strong>{'Debug Status'}</strong>
                                    <pre style={debugPreStyle}>{debugError}</pre>
                                </section>
                            )}
                            {hasInputDebug && hasOutputDebug && (
                                <div style={tabRowStyle}>
                                    <button
                                        style={getDebugTabButtonStyle(activeDebugSection === 'request')}
                                        type='button'
                                        onClick={() => setActiveDebugSection('request')}
                                    >
                                        {'요청'}
                                    </button>
                                    <button
                                        style={getDebugTabButtonStyle(activeDebugSection === 'response')}
                                        type='button'
                                        onClick={() => setActiveDebugSection('response')}
                                    >
                                        {'응답'}
                                    </button>
                                </div>
                            )}
                            {activeDebugSection === 'request' && (
                                <section style={debugPanelStyle}>
                                    <strong>{'Request Parameters'}</strong>
                                    <pre style={debugPreStyle}>{renderDebugContent(inputDebug, '요청 payload가 저장되지 않았습니다.')}</pre>
                                </section>
                            )}
                            {activeDebugSection === 'response' && (
                                <section style={debugPanelStyle}>
                                    <strong>{'Response Parameters'}</strong>
                                    <pre style={debugPreStyle}>{renderDebugContent(outputDebug, '응답 payload가 저장되지 않았습니다. 이 post가 새 디버그 저장 방식 이전에 생성되었을 수 있습니다.')}</pre>
                                </section>
                            )}
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}

function isStreamingPost(post: any) {
    return post?.props?.upstage_streaming === 'true' || post?.props?.upstage_stream_status === 'streaming';
}

function getRenderableMessage(post: any) {
    if (isUpstageAwaitingFirstChunk(post)) {
        return '';
    }

    return post?.message || '';
}

function normalizeDebugPayload(value: unknown) {
    if (typeof value !== 'string') {
        return '';
    }

    return value.trim();
}

function hasDebugFlag(value: unknown) {
    return value === true || value === 'true';
}

function firstDefinedDebug(primary: string, fallback: string) {
    if (primary !== '') {
        return primary;
    }
    return fallback;
}

function renderDebugContent(value: string, emptyMessage: string) {
    if (value !== '') {
        return value;
    }
    return emptyMessage;
}

function getDebugTabButtonStyle(active: boolean): React.CSSProperties {
    if (active) {
        return {
            ...buttonStyle,
            background: 'rgba(var(--button-bg-rgb), 0.18)',
        };
    }

    return buttonStyle;
}
