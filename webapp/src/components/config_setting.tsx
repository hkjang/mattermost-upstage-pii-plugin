import manifest from 'manifest';
import React, {useEffect, useMemo, useRef, useState} from 'react';

import type {AdminPluginConfig, BotDefinition, ConnectionStatus, PluginStatus} from '../client';
import {getAdminConfig, getStatus, testConnection} from '../client';

const defaultURL = 'http://localhost:8080/inference';
const defaultURLPlaceholder = 'http://controller-host/inference';
const defaultModel = 'pii';
const defaultSchema = 'oac';

type DraftBot = {
    local_id: string;
    username: string;
    display_name: string;
    description: string;
    base_url: string;
    auth_mode: string;
    auth_token: string;
    model: string;
    lang: string;
    schema: string;
    verbose: boolean;
    mask_sensitive_data: boolean;
    mask_pii_keys: string[];
    vllm_base_url: string;
    vllm_api_key: string;
    vllm_model: string;
    vllm_prompt: string;
    allowed_teams: string[];
    allowed_channels: string[];
    allowed_users: string[];
};

type DraftConfig = {
    service: {base_url: string; auth_mode: string; auth_token: string; allow_hosts: string};
    runtime: {default_timeout_seconds: number; max_input_length: number; max_output_length: number; mask_sensitive_data: boolean; enable_debug_logs: boolean; enable_usage_logs: boolean};
    bots: DraftBot[];
};

type Props = {
    id?: string;
    value?: unknown;
    disabled?: boolean;
    setByEnv?: boolean;
    helpText?: React.ReactNode;
    onChange: (id: string, value: unknown) => void;
    setSaveNeeded?: () => void;
};

const stack: React.CSSProperties = {display: 'flex', flexDirection: 'column', gap: 16};
const card: React.CSSProperties = {background: 'white', border: '1px solid rgba(63,67,80,.12)', borderRadius: 8, padding: 20, display: 'flex', flexDirection: 'column', gap: 12};
const row2: React.CSSProperties = {display: 'grid', gridTemplateColumns: 'repeat(2,minmax(0,1fr))', gap: 12};
const row3: React.CSSProperties = {display: 'grid', gridTemplateColumns: 'repeat(3,minmax(0,1fr))', gap: 12};
const botLayout: React.CSSProperties = {display: 'grid', gridTemplateColumns: '300px minmax(0,1fr)', gap: 16};
const field: React.CSSProperties = {width: '100%', border: '1px solid rgba(63,67,80,.16)', borderRadius: 8, padding: '10px 12px'};
const note: React.CSSProperties = {fontSize: 12, opacity: 0.75};
const box: React.CSSProperties = {padding: 12, borderRadius: 8, background: 'rgba(var(--button-bg-rgb),.08)', border: '1px solid rgba(var(--button-bg-rgb),.18)'};

const sampleBots: Partial<BotDefinition>[] = [
    {username: 'pii-masker', display_name: 'PII 마스킹 기본', description: '기본 OAC 응답으로 개인정보 필드를 추출하고 마스킹해 보여줍니다.', model: defaultModel, lang: 'ko', schema: 'oac', verbose: false, mask_sensitive_data: true},
    {username: 'pii-ufp', display_name: 'PII 구조 분석', description: 'UFP 구조와 bbox 좌표까지 확인하는 분석용 봇입니다.', model: defaultModel, lang: 'ko', schema: 'ufp', verbose: true, mask_sensitive_data: true},
];

export default function ConfigSetting(props: Props) {
    const key = props.id || 'Config';
    const disabled = Boolean(props.disabled || props.setByEnv);
    const [config, setConfig] = useState<DraftConfig>(createDefaultConfig());
    const [selected, setSelected] = useState('');
    const [status, setStatus] = useState<PluginStatus | null>(null);
    const [connection, setConnection] = useState<ConnectionStatus | null>(null);
    const [connectionError, setConnectionError] = useState('');
    const [source, setSource] = useState('config');
    const [error, setError] = useState('');
    const [loadingConfig, setLoadingConfig] = useState(true);
    const [loadingStatus, setLoadingStatus] = useState(true);
    const [testing, setTesting] = useState(false);
    const last = useRef('');

    useEffect(() => { void loadConfig(props.value, last, setConfig, setSource, setSelected, setLoadingConfig, setError); }, [props.value]);
    useEffect(() => { void loadStatus(setStatus, setLoadingStatus, setError); }, []);

    const bot = useMemo(() => config.bots.find((item) => item.local_id === selected) || config.bots[0] || null, [config.bots, selected]);
    const messages = useMemo(() => validate(config), [config]);

    const apply = (next: DraftConfig, nextSelected?: string) => {
        setConfig(next);
        const raw = JSON.stringify(buildConfig(next), null, 2);
        last.current = raw;
        props.onChange(key, raw);
        props.setSaveNeeded?.();
        setSelected(nextSelected || pickBot(next.bots, selected));
    };

    return <div style={stack}>{renderPlaceholder({bot, messages, error, loadingConfig, source, props, status, loadingStatus, connection, connectionError, testing, config, disabled, apply, setConnection, setConnectionError, setTesting, setSelected})}</div>;
}

function renderPlaceholder(args: {
    bot: DraftBot | null;
    messages: string[];
    error: string;
    loadingConfig: boolean;
    source: string;
    props: Props;
    status: PluginStatus | null;
    loadingStatus: boolean;
    connection: ConnectionStatus | null;
    connectionError: string;
    testing: boolean;
    config: DraftConfig;
    disabled: boolean;
    apply: (next: DraftConfig, nextSelected?: string) => void;
    setConnection: (value: ConnectionStatus | null) => void;
    setConnectionError: (value: string) => void;
    setTesting: (value: boolean) => void;
    setSelected: (value: string) => void;
}) {
    const {bot, messages, error, loadingConfig, source, props, status, loadingStatus, connection, connectionError, testing, config, disabled, apply, setConnection, setConnectionError, setTesting, setSelected} = args;
    const updateService = (patch: Partial<DraftConfig['service']>) => apply({...config, service: {...config.service, ...patch}});
    const updateRuntime = (patch: Partial<DraftConfig['runtime']>) => apply({...config, runtime: {...config.runtime, ...patch}});
    const updateBot = (id: string, patch: Partial<DraftBot>) => apply({...config, bots: config.bots.map((item) => item.local_id === id ? {...item, ...patch} : item)}, id);
    const test = async () => {
        setTesting(true);
        setConnection(null);
        setConnectionError('');
        try {
            setConnection(await testConnection());
        } catch (e) {
            setConnectionError((e as Error).message);
        } finally {
            setTesting(false);
        }
    };

    return <>
        <section style={card}>
            <div style={{display: 'flex', justifyContent: 'space-between', gap: 12, alignItems: 'center'}}>
                <strong>{'Upstage PII Masking 설정'}</strong>
                <span style={{fontSize: 12, fontWeight: 700}}>{manifest.version}</span>
            </div>
            <span style={note}>{'여러 Mattermost 봇에 서로 다른 PII 추론 옵션과 접근 제어를 설정할 수 있습니다.'}</span>
            <div style={box}>
                <div>{'봇은 DM 또는 @멘션 + 파일 첨부로 호출됩니다.'}</div>
                <div>{'메시지 본문은 PII API로 전달되지 않습니다. 실제 요청은 첨부 파일만 document 파트로 업로드합니다.'}</div>
                <div>{'model은 query string과 form-data에 같은 값으로 넣어 호출합니다.'}</div>
                <div>{'schema=oac 는 일반 서비스 연동, schema=ufp 는 구조 분석용 응답에 적합합니다.'}</div>
                <div>{'verbose=true 는 bounding box 좌표를 포함하므로 응답 크기가 커질 수 있습니다.'}</div>
                <div>{'vLLM 후처리를 켜면 추출된 필드 요약과 사용자 메시지를 함께 보내 최종 응답을 생성합니다. 프롬프트에서 {{user_message}}, {{document_text}} 치환자를 사용할 수 있습니다.'}</div>
            </div>
            {source === 'legacy' && <div style={box}>{'기존 개별 설정을 불러왔습니다. 저장하면 단일 Config 형식으로 정리됩니다.'}</div>}
            {props.setByEnv && <div style={box}>{'이 설정은 환경 변수로 관리되고 있어 여기에서 수정할 수 없습니다.'}</div>}
            {props.helpText}
            {error && <div style={box}>{error}</div>}
            {messages.length > 0 && <div style={box}>{messages.map((message) => <div key={message}>{message}</div>)}</div>}
        </section>

        <section style={card}>
            <strong>{'서비스 연결'}</strong>
            {loadingConfig ? <span>{'설정을 불러오는 중입니다...'}</span> : <>
                <div style={row2}>
                    <Field label={'기본 URL'}><input disabled={disabled} style={field} value={config.service.base_url} placeholder={defaultURLPlaceholder} onChange={(e) => updateService({base_url: e.target.value})}/></Field>
                    <Field label={'인증 방식'}>
                        <select disabled={disabled} style={field} value={config.service.auth_mode} onChange={(e) => updateService({auth_mode: e.target.value})}>
                            <option value='bearer'>{'Authorization: Bearer'}</option>
                            <option value='x-api-key'>{'x-api-key'}</option>
                        </select>
                    </Field>
                </div>
                <div style={row2}>
                    <Field label={'기본 API 키'}><input disabled={disabled} type='password' style={field} value={config.service.auth_token} onChange={(e) => updateService({auth_token: e.target.value})}/></Field>
                    <Field label={'허용 호스트'}><input disabled={disabled} style={field} value={config.service.allow_hosts} placeholder={'controller-host'} onChange={(e) => updateService({allow_hosts: e.target.value})}/></Field>
                </div>
                <div style={row3}>
                    <Field label={'타임아웃(초)'}><input disabled={disabled} type='number' min={1} style={field} value={String(config.runtime.default_timeout_seconds)} onChange={(e) => updateRuntime({default_timeout_seconds: num(e.target.value, 30)})}/></Field>
                    <Field label={'최대 메시지 길이'}><input disabled={disabled} type='number' min={1} style={field} value={String(config.runtime.max_input_length)} onChange={(e) => updateRuntime({max_input_length: num(e.target.value, 4000)})}/></Field>
                    <Field label={'최대 응답 길이'}><input disabled={disabled} type='number' min={1} style={field} value={String(config.runtime.max_output_length)} onChange={(e) => updateRuntime({max_output_length: num(e.target.value, 8000)})}/></Field>
                </div>
                <label><input disabled={disabled} type='checkbox' checked={config.runtime.mask_sensitive_data} onChange={(e) => updateRuntime({mask_sensitive_data: e.target.checked})}/>{' 기본 개인정보 마스킹'}</label>
                <label><input disabled={disabled} type='checkbox' checked={config.runtime.enable_debug_logs} onChange={(e) => updateRuntime({enable_debug_logs: e.target.checked})}/>{' 디버그 로그'}</label>
                <label><input disabled={disabled} type='checkbox' checked={config.runtime.enable_usage_logs} onChange={(e) => updateRuntime({enable_usage_logs: e.target.checked})}/>{' 사용량 로그'}</label>
                <span style={note}>{'기본 마스킹은 안전을 위해 켜져 있습니다. 허용 호스트에는 PII API와 vLLM 호스트를 모두 넣을 수 있습니다.'}</span>
            </>}
        </section>

        <section style={card}>
            <div style={{display: 'flex', justifyContent: 'space-between', gap: 12, alignItems: 'center'}}>
                <strong>{'봇 카탈로그'}</strong>
                <div style={{display: 'flex', gap: 8}}>
                    <button className='btn btn-tertiary' disabled={disabled} type='button' onClick={() => apply({...config, bots: sampleBots.map((item, index) => normalizeBot(item, index, config.runtime.mask_sensitive_data))}, 'bot-0')}>{'예시 불러오기'}</button>
                    <button className='btn btn-primary' disabled={disabled} type='button' onClick={() => { const next = emptyBot(config.runtime.mask_sensitive_data); apply({...config, bots: [...config.bots, next]}, next.local_id); }}>{'봇 추가'}</button>
                </div>
            </div>
            <div style={botLayout}>
                <div style={{display: 'flex', flexDirection: 'column', gap: 8}}>
                    {config.bots.length === 0 && <div style={box}>{'아직 등록된 봇이 없습니다.'}</div>}
                    {config.bots.map((item) => <button key={item.local_id} type='button' onClick={() => setSelected(item.local_id)} style={{...box, textAlign: 'left', borderColor: bot?.local_id === item.local_id ? 'rgba(var(--button-bg-rgb),.5)' : 'transparent'}}><strong>{item.display_name || '@new-bot'}</strong><div>{`@${item.username || 'username'}`}</div><div style={note}>{`${item.model} | ${item.schema}${item.lang ? ` | ${item.lang}` : ''}${item.verbose ? ' | bbox' : ''}${item.vllm_model ? ` | vLLM=${item.vllm_model}` : ''}`}</div></button>)}
                </div>
                <div style={{display: 'flex', flexDirection: 'column', gap: 12}}>
                    {!bot && <div style={box}>{'왼쪽에서 봇을 선택하세요.'}</div>}
                    {bot && <>
                        <div style={{display: 'flex', justifyContent: 'space-between', gap: 12}}>
                            <strong>{bot.display_name || '@new-bot'}</strong>
                            <div style={{display: 'flex', gap: 8}}>
                                <button className='btn btn-tertiary' disabled={disabled} type='button' onClick={() => { const copy = {...bot, local_id: id('bot'), username: bot.username ? `${bot.username}-copy` : '', display_name: bot.display_name ? `${bot.display_name} 복사본` : '', allowed_teams: [...bot.allowed_teams], allowed_channels: [...bot.allowed_channels], allowed_users: [...bot.allowed_users]}; apply({...config, bots: [...config.bots, copy]}, copy.local_id); }}>{'복제'}</button>
                                <button className='btn btn-danger' disabled={disabled} type='button' onClick={() => apply({...config, bots: config.bots.filter((item) => item.local_id !== bot.local_id)})}>{'삭제'}</button>
                            </div>
                        </div>
                        <div style={row2}>
                            <Field label={'username'}><input disabled={disabled} style={field} value={bot.username} placeholder={'pii-masker'} onChange={(e) => updateBot(bot.local_id, {username: user(e.target.value)})}/></Field>
                            <Field label={'표시 이름'}><input disabled={disabled} style={field} value={bot.display_name} onChange={(e) => updateBot(bot.local_id, {display_name: e.target.value})}/></Field>
                        </div>
                        <Field label={'설명'}><textarea disabled={disabled} style={{...field, minHeight: 72}} value={bot.description} onChange={(e) => updateBot(bot.local_id, {description: e.target.value})}/></Field>
                        <div style={row3}>
                            <Field label={'model'}><input disabled={disabled} style={field} value={bot.model} onChange={(e) => updateBot(bot.local_id, {model: e.target.value || defaultModel})}/></Field>
                            <Field label={'lang'}>
                                <select disabled={disabled} style={field} value={bot.lang} onChange={(e) => updateBot(bot.local_id, {lang: lang(e.target.value)})}>
                                    <option value=''>{'자동 / 비움'}</option>
                                    <option value='ko'>{'ko'}</option>
                                    <option value='en'>{'en'}</option>
                                    <option value='ja'>{'ja'}</option>
                                    <option value='zh'>{'zh'}</option>
                                </select>
                            </Field>
                            <Field label={'schema'}>
                                <select disabled={disabled} style={field} value={bot.schema} onChange={(e) => updateBot(bot.local_id, {schema: schema(e.target.value)})}>
                                    <option value='oac'>{'oac'}</option>
                                    <option value='ufp'>{'ufp'}</option>
                                </select>
                            </Field>
                        </div>
                        <div style={row2}>
                            <Field label={'봇 전용 URL'}><input disabled={disabled} style={field} value={bot.base_url} placeholder={'비워 두면 기본 URL 사용'} onChange={(e) => updateBot(bot.local_id, {base_url: e.target.value})}/></Field>
                            <Field label={'봇 전용 API 키'}><input disabled={disabled} type='password' style={field} value={bot.auth_token} placeholder={'비워 두면 기본 키 사용'} onChange={(e) => updateBot(bot.local_id, {auth_token: e.target.value})}/></Field>
                        </div>
                        <Field label={'봇 전용 인증 방식'}>
                            <select disabled={disabled} style={field} value={bot.auth_mode} onChange={(e) => updateBot(bot.local_id, {auth_mode: botAuth(e.target.value)})}>
                                <option value=''>{'기본값 사용'}</option>
                                <option value='bearer'>{'Authorization: Bearer'}</option>
                                <option value='x-api-key'>{'x-api-key'}</option>
                            </select>
                        </Field>
                        <label><input disabled={disabled} type='checkbox' checked={bot.verbose} onChange={(e) => updateBot(bot.local_id, {verbose: e.target.checked})}/>{' verbose (bounding box 포함)'}</label>
                        <label><input disabled={disabled} type='checkbox' checked={bot.mask_sensitive_data} onChange={(e) => updateBot(bot.local_id, {mask_sensitive_data: e.target.checked})}/>{' 개인정보 마스킹'}</label>
                        <span style={note}>{'마스킹 대상: 이메일 주소, 한국 휴대폰/전화번호, 주민등록번호, 13자리 이상 카드/계좌/식별번호 형태의 숫자열'}</span>
                        <Field label={'파일 마스킹 PII 키'}>
                            <input disabled={disabled} style={field} value={join(bot.mask_pii_keys)} placeholder={'* 또는 개인정보.이름, 개인정보.주민등록번호'} onChange={(e) => updateBot(bot.local_id, {mask_pii_keys: split(e.target.value)})}/>
                        </Field>
                        <span style={note}>{'boundingBox가 있는 PII 필드를 원본 파일(이미지/PDF)에 검은 박스로 마스킹합니다. * 입력 시 모든 필드, 쉼표로 구분하여 특정 필드만 지정 가능. 비워두면 파일 마스킹 비활성화.'}</span>
                        <div style={{...box, display: 'flex', flexDirection: 'column', gap: 8}}>
                            <strong>{'vLLM 후처리'}</strong>
                            <span style={note}>{'vLLM URL과 모델을 입력하면 추출된 PII 필드 요약을 기반으로 한 번 더 LLM 응답을 생성합니다. 프롬프트에서 {{user_message}}, {{document_text}} 를 사용할 수 있습니다.'}</span>
                            <div style={row2}>
                                <Field label={'vLLM URL'}><input disabled={disabled} style={field} value={bot.vllm_base_url} placeholder={'http://localhost:8000/v1'} onChange={(e) => updateBot(bot.local_id, {vllm_base_url: e.target.value})}/></Field>
                                <Field label={'vLLM API Key'}><input disabled={disabled} type='password' style={field} value={bot.vllm_api_key} placeholder={'비워 두면 Authorization 헤더 없이 호출'} onChange={(e) => updateBot(bot.local_id, {vllm_api_key: e.target.value})}/></Field>
                            </div>
                            <Field label={'vLLM Model'}><input disabled={disabled} style={field} value={bot.vllm_model} placeholder={'Qwen/Qwen2.5-7B-Instruct'} onChange={(e) => updateBot(bot.local_id, {vllm_model: e.target.value})}/></Field>
                            <Field label={'vLLM Prompt'}><textarea disabled={disabled} style={{...field, minHeight: 120}} value={bot.vllm_prompt} placeholder={'추출된 개인정보 필드를 한국어로 요약해줘.\n\n사용자 요청:\n{{user_message}}\n\nPII 결과:\n{{document_text}}'} onChange={(e) => updateBot(bot.local_id, {vllm_prompt: e.target.value})}/></Field>
                        </div>
                        <div style={row3}>
                            <Field label={'허용 팀'}><input disabled={disabled} style={field} value={join(bot.allowed_teams)} placeholder={'engineering'} onChange={(e) => updateBot(bot.local_id, {allowed_teams: split(e.target.value, true)})}/></Field>
                            <Field label={'허용 채널'}><input disabled={disabled} style={field} value={join(bot.allowed_channels)} placeholder={'town-square'} onChange={(e) => updateBot(bot.local_id, {allowed_channels: split(e.target.value, true)})}/></Field>
                            <Field label={'허용 사용자'}><input disabled={disabled} style={field} value={join(bot.allowed_users)} placeholder={'alice'} onChange={(e) => updateBot(bot.local_id, {allowed_users: split(e.target.value, true)})}/></Field>
                        </div>
                        <pre style={{...box, whiteSpace: 'pre-wrap', fontSize: 12}}>{curl(config, bot)}</pre>
                    </>}
                </div>
            </div>
        </section>

        <section style={card}>
            <strong>{'현재 상태'}</strong>
            {loadingStatus ? <span>{'플러그인 상태를 불러오는 중입니다...'}</span> : <>
                {status && <div style={box}><div>{`기본 URL: ${status.base_url || '설정되지 않음'}`}</div><div>{`봇 수: ${status.bot_count}`}</div><div>{`허용 호스트: ${(status.allow_hosts || []).join(', ') || '기본 URL 호스트 사용'}`}</div>{status.config_error && <div>{`설정 오류: ${status.config_error}`}</div>}{status.bot_sync?.last_error && <div>{`동기화 오류: ${status.bot_sync.last_error}`}</div>}</div>}
                <button className='btn btn-primary' disabled={testing} type='button' onClick={test}>{testing ? '연결 확인 중...' : '연결 테스트'}</button>
                {connection && <div style={box}><div>{connection.ok ? '연결에 성공했습니다.' : '연결에 실패했습니다.'}</div><div>{connection.url}</div><div style={{whiteSpace: 'pre-wrap'}}>{connection.message}</div>{connection.error_code && <div>{`오류 코드: ${connection.error_code}`}</div>}{connection.detail && <div style={{whiteSpace: 'pre-wrap'}}>{connection.detail}</div>}{connection.hint && <div style={{whiteSpace: 'pre-wrap'}}>{connection.hint}</div>}</div>}
                {connectionError && <div style={box}><div>{'연결 테스트 중 오류가 발생했습니다.'}</div><div style={{whiteSpace: 'pre-wrap'}}>{connectionError}</div></div>}
            </>}
        </section>

        <details style={card}><summary>{'JSON 미리보기'}</summary><pre style={{...box, whiteSpace: 'pre-wrap', fontSize: 12}}>{JSON.stringify(buildConfig(config), null, 2)}</pre></details>
    </>;
}

function Field(props: {label: string; children: React.ReactNode}) {
    return <label style={{display: 'flex', flexDirection: 'column', gap: 6}}><strong>{props.label}</strong>{props.children}</label>;
}

function createDefaultConfig(): DraftConfig {
    return {
        service: {base_url: defaultURL, auth_mode: 'bearer', auth_token: '', allow_hosts: ''},
        runtime: {default_timeout_seconds: 30, max_input_length: 4000, max_output_length: 8000, mask_sensitive_data: true, enable_debug_logs: false, enable_usage_logs: true},
        bots: [],
    };
}

async function loadConfig(value: unknown, last: React.MutableRefObject<string>, setConfig: (value: DraftConfig) => void, setSource: (value: string) => void, setSelected: (value: string | ((value: string) => string)) => void, setLoading: (value: boolean) => void, setError: (value: string) => void) {
    setLoading(true);
    setError('');
    const raw = serialize(value);
    if (raw && raw === last.current) {
        setLoading(false);
        return;
    }
    const parsed = parseValue(value);
    if (parsed.ok) {
        setConfig(parsed.config);
        setSource('config');
        setSelected((current) => pickBot(parsed.config.bots, current));
        last.current = raw;
        setLoading(false);
        return;
    }
    try {
        const response = await getAdminConfig();
        const next = normalizeConfig(response.config);
        setConfig(next);
        setSource(response.source || 'config');
        setSelected((current) => pickBot(next.bots, current));
        last.current = serialize(buildConfig(next));
    } catch (e) {
        setError((e as Error).message);
    } finally {
        setLoading(false);
    }
}

async function loadStatus(setStatus: (value: PluginStatus | null) => void, setLoading: (value: boolean) => void, setError: (value: string) => void) {
    setLoading(true);
    try {
        setStatus(await getStatus());
    } catch (e) {
        setError((e as Error).message);
    } finally {
        setLoading(false);
    }
}

function parseValue(value: unknown) {
    if (value == null || value === '') {
        return {ok: false, config: createDefaultConfig()};
    }
    try {
        return {ok: true, config: normalizeConfig((typeof value === 'string' ? JSON.parse(value) : value) as AdminPluginConfig)};
    } catch {
        return {ok: false, config: createDefaultConfig()};
    }
}

function normalizeConfig(value?: AdminPluginConfig): DraftConfig {
    const next = createDefaultConfig();
    if (!value) {
        return next;
    }
    next.service = {
        base_url: text(value.service?.base_url) || defaultURL,
        auth_mode: auth(text(value.service?.auth_mode)),
        auth_token: text(value.service?.auth_token),
        allow_hosts: text(value.service?.allow_hosts),
    };
    next.runtime = {
        default_timeout_seconds: num(value.runtime?.default_timeout_seconds, 30),
        max_input_length: num(value.runtime?.max_input_length, 4000),
        max_output_length: num(value.runtime?.max_output_length, 8000),
        mask_sensitive_data: value.runtime?.mask_sensitive_data ?? true,
        enable_debug_logs: Boolean(value.runtime?.enable_debug_logs),
        enable_usage_logs: value.runtime?.enable_usage_logs ?? true,
    };
    next.bots = Array.isArray(value.bots) ? value.bots.map((item, index) => normalizeBot(item, index, next.runtime.mask_sensitive_data)) : [];
    return next;
}

function buildConfig(config: DraftConfig): AdminPluginConfig {
    return {
        service: {base_url: config.service.base_url.trim(), auth_mode: auth(config.service.auth_mode), auth_token: config.service.auth_token.trim(), allow_hosts: config.service.allow_hosts.trim()},
        runtime: {default_timeout_seconds: num(config.runtime.default_timeout_seconds, 30), max_input_length: num(config.runtime.max_input_length, 4000), max_output_length: num(config.runtime.max_output_length, 8000), mask_sensitive_data: Boolean(config.runtime.mask_sensitive_data), enable_debug_logs: Boolean(config.runtime.enable_debug_logs), enable_usage_logs: Boolean(config.runtime.enable_usage_logs)},
        bots: config.bots.map((item) => ({
            id: item.username.trim(),
            username: item.username.trim(),
            display_name: item.display_name.trim(),
            description: item.description.trim(),
            base_url: item.base_url.trim(),
            auth_mode: botAuth(item.auth_mode),
            auth_token: item.auth_token.trim(),
            model: item.model.trim() || defaultModel,
            lang: lang(item.lang),
            schema: schema(item.schema),
            verbose: Boolean(item.verbose),
            mask_sensitive_data: Boolean(item.mask_sensitive_data),
            mask_pii_keys: item.mask_pii_keys.filter((k) => k.trim() !== ''),
            vllm_base_url: item.vllm_base_url.trim(),
            vllm_api_key: item.vllm_api_key.trim(),
            vllm_model: item.vllm_model.trim(),
            vllm_prompt: item.vllm_prompt.trim(),
            allowed_teams: split(join(item.allowed_teams), true),
            allowed_channels: split(join(item.allowed_channels), true),
            allowed_users: split(join(item.allowed_users), true),
        })),
    };
}

function normalizeBot(value: Partial<BotDefinition>, index = 0, inheritedMaskSensitive = true): DraftBot {
    return {
        local_id: `bot-${index}`,
        username: user(text(value.username)),
        display_name: text(value.display_name),
        description: text(value.description),
        base_url: text(value.base_url),
        auth_mode: botAuth(text(value.auth_mode)),
        auth_token: text(value.auth_token),
        model: text(value.model) || defaultModel,
        lang: lang(text(value.lang)),
        schema: schema(text(value.schema)),
        verbose: Boolean(value.verbose),
        mask_sensitive_data: value.mask_sensitive_data ?? inheritedMaskSensitive,
        mask_pii_keys: Array.isArray(value.mask_pii_keys) ? value.mask_pii_keys.map(String) : [],
        vllm_base_url: text(value.vllm_base_url),
        vllm_api_key: text(value.vllm_api_key),
        vllm_model: text(value.vllm_model),
        vllm_prompt: text(value.vllm_prompt),
        allowed_teams: split(join(Array.isArray(value.allowed_teams) ? value.allowed_teams : []), true),
        allowed_channels: split(join(Array.isArray(value.allowed_channels) ? value.allowed_channels : []), true),
        allowed_users: split(join(Array.isArray(value.allowed_users) ? value.allowed_users : []), true),
    };
}

function validate(config: DraftConfig) {
    const items: string[] = [];
    const names = new Set<string>();
    if (!config.service.base_url.trim()) {
        items.push('기본 URL은 필수입니다.');
    }
    config.bots.forEach((bot, index) => {
        const label = bot.display_name || bot.username || `봇 ${index + 1}`;
        if (!bot.username.trim()) {
            items.push(`${label}: username은 필수입니다.`);
        } else if (names.has(bot.username.trim())) {
            items.push(`${label}: username이 중복되었습니다.`);
        } else {
            names.add(bot.username.trim());
        }
        if (!bot.display_name.trim()) {
            items.push(`${label}: 표시 이름은 필수입니다.`);
        }
        if (!bot.model.trim()) {
            items.push(`${label}: model은 필수입니다.`);
        }
        if (!['', 'ko', 'en', 'ja', 'zh'].includes(bot.lang)) {
            items.push(`${label}: lang는 ko, en, ja, zh 또는 비움만 지원합니다.`);
        }
        if (!['oac', 'ufp'].includes(bot.schema)) {
            items.push(`${label}: schema는 oac 또는 ufp만 지원합니다.`);
        }
        const hasVLLMFields = [bot.vllm_base_url, bot.vllm_api_key, bot.vllm_model, bot.vllm_prompt].some((item) => item.trim() !== '');
        if (hasVLLMFields && !bot.vllm_base_url.trim()) {
            items.push(`${label}: vLLM을 쓰려면 URL이 필요합니다.`);
        }
        if (bot.vllm_base_url.trim() && !bot.vllm_model.trim()) {
            items.push(`${label}: vLLM URL을 입력했다면 vLLM model도 필요합니다.`);
        }
    });
    return items;
}

function curl(config: DraftConfig, bot: DraftBot) {
    const authLine = (bot.auth_mode || config.service.auth_mode) === 'x-api-key' ? '-H "x-api-key: $PII_API_KEY"' : '-H "Authorization: Bearer $PII_API_KEY"';
    const lines = [
        `curl -X POST "${buildCurlURL(bot.base_url || config.service.base_url || defaultURL, bot.model || defaultModel)}"`,
        authLine,
        '-H "Accept: application/json"',
        `-F "model=${bot.model || defaultModel}"`,
        '-F "document=@example.pdf"',
    ];
    if (bot.lang) {
        lines.push(`-F "lang=${bot.lang}"`);
    }
    if (schema(bot.schema) !== defaultSchema) {
        lines.push(`-F "schema=${schema(bot.schema)}"`);
    }
    if (bot.verbose) {
        lines.push('-F "verbose=true"');
    }
    return lines.map((line, index) => {
        const prefix = index === 0 ? '' : '  ';
        return index < lines.length - 1 ? `${prefix}${line} \\` : `${prefix}${line}`;
    }).join('\n');
}

function buildCurlURL(baseURL: string, model: string) {
    const trimmed = (baseURL || defaultURL).trim();
    try {
        const parsed = new URL(trimmed);
        parsed.searchParams.set('model', model || defaultModel);
        return parsed.toString();
    } catch {
        const separator = trimmed.includes('?') ? '&' : '?';
        return `${trimmed}${separator}model=${encodeURIComponent(model || defaultModel)}`;
    }
}

function emptyBot(inheritedMaskSensitive = true): DraftBot {
    return {
        local_id: id('bot'),
        username: '',
        display_name: '',
        description: '',
        base_url: '',
        auth_mode: '',
        auth_token: '',
        model: defaultModel,
        lang: '',
        schema: defaultSchema,
        verbose: false,
        mask_sensitive_data: inheritedMaskSensitive,
        mask_pii_keys: [],
        vllm_base_url: '',
        vllm_api_key: '',
        vllm_model: '',
        vllm_prompt: '',
        allowed_teams: [],
        allowed_channels: [],
        allowed_users: [],
    };
}

function pickBot(bots: DraftBot[], current: string) {
    return current && bots.some((bot) => bot.local_id === current) ? current : (bots[0]?.local_id || '');
}

function serialize(value: unknown) { try { return value == null || value === '' ? '' : typeof value === 'string' ? value : JSON.stringify(value); } catch { return ''; } }
function text(value: unknown) { return typeof value === 'string' ? value : value == null ? '' : String(value); }
function num(value: unknown, fallback: number) { const parsed = Number(value); return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback; }
function auth(value: string) { return value === 'x-api-key' ? 'x-api-key' : 'bearer'; }
function botAuth(value: string) { return value === 'x-api-key' ? 'x-api-key' : value === 'bearer' ? 'bearer' : ''; }
function lang(value: string) { return ['ko', 'en', 'ja', 'zh'].includes(value) ? value : ''; }
function schema(value: string) { return value === 'ufp' ? 'ufp' : 'oac'; }
function user(value: string) { return value.toLowerCase().replace(/[^a-z0-9-_]/g, ''); }
function id(prefix: string) { return typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function' ? `${prefix}-${crypto.randomUUID()}` : `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
function join(values: string[]) { return values.join(', '); }
function split(value: string, lower = false) { return value.split(/[\r\n,]+/).map((item) => lower ? item.trim().toLowerCase() : item.trim()).filter(Boolean).filter((item, index, all) => all.indexOf(item) === index); }
