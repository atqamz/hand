use std::ffi::{CString, c_char};
use std::io;
use std::mem;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::path::PathBuf;
use std::sync::{Arc, LazyLock, Mutex};
use std::time::Duration;

use hand_android_core::{
    Alert, AlertSink, Cancel, CipherError, Companion, CompanionConfig, Feed, Fleet, Jar, KeyCipher,
    Live, LiveSink, Method, OPEN_REJECTED, POLL_HOST, PairError, Polled, RESCAN, Reporter, Request,
    RequestError, Response, Route, Sessions, Supervisor, VERSION, open_paired,
};
use jni::errors::LogErrorAndDefault;
use jni::objects::{Global, JByteArray, JClass, JObject, JString, JValue};
use jni::sys::{jboolean, jlong};
use jni::{Env, EnvUnowned, JavaVM, jni_sig, jni_str};

const REPORT: Duration = Duration::from_secs(60);
const PROBE: i32 = 0;
const SEAL: i32 = 1;
const OPEN: i32 = 2;

type Alerts = Supervisor<Arc<Sessions<HostJar>>, HostSink>;

struct Core {
    companion: Arc<Companion>,
    alerts: Arc<Alerts>,
    sessions: Arc<Sessions<HostJar>>,
    host: Arc<Host>,
    route: Route,
    live: Mutex<Option<RunningLive>>,
    reporter: Mutex<Option<Reporter>>,
}

struct RunningLive {
    fleet: String,
    task: tokio::task::JoinHandle<()>,
    cancel: Cancel,
}

impl RunningLive {
    fn stop(self) {
        self.cancel.cancel();
        self.task.abort();
    }
}

impl Core {
    fn start_live(&self, fleet: &str) {
        let mut slot = self.live.lock().unwrap();
        if let Some(running) = slot.take() {
            running.stop();
        }
        let live = Live::routed(self.route.clone());
        let sessions = self.sessions.clone();
        let sink = HostLive(self.host.clone());
        let cancel = Cancel::default();
        let token = cancel.clone();
        let id = fleet.to_owned();
        let task = runtime().spawn(async move {
            let _ = live.run(&id, &sessions, &sink, &token).await;
        });
        *slot = Some(RunningLive {
            fleet: fleet.to_owned(),
            task,
            cancel,
        });
    }

    fn stop_live(&self) {
        if let Some(running) = self.live.lock().unwrap().take() {
            running.stop();
        }
    }

    fn stop_live_of(&self, fleets: &[String]) {
        let mut slot = self.live.lock().unwrap();
        if slot
            .as_ref()
            .is_some_and(|running| fleets.contains(&running.fleet))
            && let Some(running) = slot.take()
        {
            running.stop();
        }
    }

    fn quiet(&self) {
        self.stop_live();
        self.reporter.lock().unwrap().take();
        self.alerts.pause();
    }
}

struct State {
    core: Option<Arc<Core>>,
}

static STATE: Mutex<State> = Mutex::new(State { core: None });
static RUNTIME: LazyLock<tokio::runtime::Runtime> = LazyLock::new(|| {
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .expect("the core runtime builds")
});

fn runtime() -> &'static tokio::runtime::Runtime {
    &RUNTIME
}

pub fn forget_alerts() -> io::Result<()> {
    let core = STATE.lock().unwrap().core.clone();
    let Some(core) = core else {
        return Ok(());
    };
    core.stop_live();
    core.alerts.forget_all();
    let cleared = core.sessions.forget();
    let _ = core.host.stop_service();
    if cleared.is_err() {
        core.alerts.stop();
        warn("the web view's cookies or the session store could not be cleared");
    }
    cleared
}

pub fn forget_host_alerts(endpoint: &str) -> io::Result<()> {
    let core = STATE.lock().unwrap().core.clone();
    let Some(core) = core else {
        return Ok(());
    };
    let fleets: Vec<String> = core
        .sessions
        .hosts()
        .into_iter()
        .filter(|host| host.endpoint == endpoint)
        .flat_map(|host| host.fleets.into_iter().map(|fleet| fleet.id))
        .collect();
    core.stop_live_of(&fleets);
    core.alerts.forget_fleets(&fleets);
    let cleared = core.sessions.forget_host(endpoint);
    if core.sessions.listed().is_empty() {
        let _ = core.host.stop_service();
    }
    if cleared.is_err() {
        core.alerts.stop();
        if core.reporter.lock().unwrap().is_some() {
            core.alerts.start();
        }
        warn("the web view's cookies or the session store could not be cleared");
    }
    cleared
}

unsafe extern "C" {
    fn __android_log_write(priority: i32, tag: *const c_char, text: *const c_char) -> i32;
}

fn log(priority: i32, message: &str) {
    if let Ok(text) = CString::new(message) {
        unsafe { __android_log_write(priority, c"HandAlerts".as_ptr(), text.as_ptr()) };
    }
}

fn warn(message: &str) {
    log(5, message);
}

fn info(message: &str) {
    log(4, message);
}

struct Host {
    vm: JavaVM,
    object: Global<JObject<'static>>,
}

impl Host {
    fn call<R>(&self, call: impl FnOnce(&mut Env) -> jni::errors::Result<R>) -> io::Result<R> {
        self.vm
            .attach_current_thread(call)
            .map_err(io::Error::other)
    }

    fn keystore(&self, operation: i32, data: &[u8]) -> io::Result<Vec<u8>> {
        self.call(|env| {
            let input = env.byte_array_from_slice(data)?;
            let output = env
                .call_method(
                    &self.object,
                    jni_str!("keystore"),
                    jni_sig!("(I[B)[B"),
                    &[JValue::Int(operation), JValue::Object(&input)],
                )?
                .l()?;
            let output = env.cast_local::<JByteArray>(output)?;
            env.convert_byte_array(output)
        })
    }

    fn cookies(&self) -> io::Result<Option<String>> {
        self.call(|env| {
            let header = env
                .call_method(
                    &self.object,
                    jni_str!("cookies"),
                    jni_sig!("()Ljava/lang/String;"),
                    &[],
                )?
                .l()?;
            if header.as_raw().is_null() {
                return Ok(None);
            }
            let header = env.cast_local::<JString>(header)?;
            Ok(Some(header.try_to_string(env)?))
        })
    }

    fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<bool> {
        self.call(|env| {
            let id = env.new_string(&fleet.id)?;
            let name = env.new_string(&fleet.name)?;
            let kind = env.new_string(&alert.kind)?;
            let reference = env.new_string(&alert.reference)?;
            let text = env.new_string(&alert.text)?;
            let link = match &alert.link {
                Some(link) => JObject::from(env.new_string(link)?),
                None => JObject::null(),
            };
            env.call_method(
                &self.object,
                jni_str!("alert"),
                jni_sig!("(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)Z"),
                &[
                    JValue::Object(&id),
                    JValue::Object(&name),
                    JValue::Object(&kind),
                    JValue::Object(&reference),
                    JValue::Object(&text),
                    JValue::Object(&link),
                ],
            )?
            .z()
        })
    }

    fn clear_cookies(&self) -> io::Result<bool> {
        self.call(|env| {
            env.call_method(&self.object, jni_str!("clearCookies"), jni_sig!("()Z"), &[])?
                .z()
        })
    }

    fn unreadable(&self) -> io::Result<()> {
        self.call(|env| {
            env.call_method(&self.object, jni_str!("unreadable"), jni_sig!("()V"), &[])?
                .v()
        })
    }

    fn stop_service(&self) -> io::Result<()> {
        self.call(|env| {
            env.call_method(&self.object, jni_str!("stopService"), jni_sig!("()V"), &[])?
                .v()
        })
    }

    fn unpaired(&self, fleet: &str, name: Option<&str>) -> io::Result<()> {
        self.call(|env| {
            let id = env.new_string(fleet)?;
            let name = match name {
                Some(name) => JObject::from(env.new_string(name)?),
                None => JObject::null(),
            };
            env.call_method(
                &self.object,
                jni_str!("unpaired"),
                jni_sig!("(Ljava/lang/String;Ljava/lang/String;)V"),
                &[JValue::Object(&id), JValue::Object(&name)],
            )?
            .v()
        })
    }

    fn live(&self, fleet: &str, topic: &str, json: &str) -> io::Result<()> {
        self.call(|env| {
            let fleet = env.new_string(fleet)?;
            let topic = env.new_string(topic)?;
            let json = env.new_string(json)?;
            env.call_method(
                &self.object,
                jni_str!("live"),
                jni_sig!("(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)V"),
                &[
                    JValue::Object(&fleet),
                    JValue::Object(&topic),
                    JValue::Object(&json),
                ],
            )?
            .v()
        })
    }

    fn link(&self, fleet: &str, state: &str, since: u64) -> io::Result<()> {
        self.call(|env| {
            let fleet = env.new_string(fleet)?;
            let state = env.new_string(state)?;
            env.call_method(
                &self.object,
                jni_str!("link"),
                jni_sig!("(Ljava/lang/String;Ljava/lang/String;J)V"),
                &[
                    JValue::Object(&fleet),
                    JValue::Object(&state),
                    JValue::Long(since as jlong),
                ],
            )?
            .v()
        })
    }
}

struct HostLive(Arc<Host>);

impl LiveSink for HostLive {
    fn live(&self, fleet: &str, topic: &str, json: &str) -> io::Result<()> {
        self.0.live(fleet, topic, json)
    }

    fn link(&self, fleet: &str, state: &str, since: u64) -> io::Result<()> {
        self.0.link(fleet, state, since)
    }
}

struct HostCipher(Arc<Host>);

impl HostCipher {
    fn run(&self, operation: i32, data: &[u8]) -> Result<Vec<u8>, CipherError> {
        let reply = self.0.keystore(operation, data)?;
        match reply.split_first() {
            Some((0, payload)) => Ok(payload.to_vec()),
            Some((_, failure)) => {
                let failure = String::from_utf8_lossy(failure);
                let (code, message) = failure.split_once('\n').unwrap_or((&failure, ""));
                Err(CipherError::from_plugin(Some(code), Some(message)))
            }
            None => Err(CipherError::Io(io::Error::other(
                "the Android Keystore answered nothing",
            ))),
        }
    }
}

impl KeyCipher for HostCipher {
    fn seal(&self, plaintext: &[u8]) -> Result<Vec<u8>, CipherError> {
        self.run(SEAL, plaintext)
    }

    fn open(&self, sealed: &[u8]) -> Result<Vec<u8>, CipherError> {
        self.run(OPEN, sealed)
    }

    fn probe(&self) -> Result<(), CipherError> {
        self.run(PROBE, &[]).map(|_| ())
    }
}

struct HostJar(Arc<Host>);

impl Jar for HostJar {
    fn cookies(&self) -> io::Result<Option<String>> {
        self.0.cookies()
    }

    fn clear(&self) -> io::Result<()> {
        if self.0.clear_cookies()? {
            Ok(())
        } else {
            Err(io::Error::other("the web view's cookies were not cleared"))
        }
    }
}

struct HostSink(Arc<Host>);

impl AlertSink for HostSink {
    async fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<()> {
        if self.0.alert(fleet, alert)? {
            Ok(())
        } else {
            Err(io::Error::other("the notification was not posted"))
        }
    }

    async fn unpaired(&self, fleet: &str, name: Option<&str>) {
        let _ = self.0.unpaired(fleet, name);
    }
}

fn ensure_context(env: &mut Env, context: &JObject) -> Result<(), String> {
    if catch_unwind(ndk_context::android_context).is_ok() {
        return Ok(());
    }
    let vm = env.get_java_vm().map_err(|error| error.to_string())?;
    let global = env
        .new_global_ref(context)
        .map_err(|error| error.to_string())?;
    unsafe {
        ndk_context::initialize_android_context(vm.get_raw().cast(), global.as_raw().cast());
    }
    mem::forget(global);
    Ok(())
}

fn start(
    env: &mut Env,
    data_dir: &JString,
    host: &JObject,
    context: &JObject,
) -> Result<(), String> {
    let mut state = STATE.lock().unwrap();
    if state.core.is_some() {
        return Ok(());
    }
    let result: Result<Core, String> = (|| {
        let data_dir = PathBuf::from(
            data_dir
                .try_to_string(env)
                .map_err(|error| error.to_string())?,
        );
        ensure_context(env, context)?;
        let host = Arc::new(Host {
            vm: env.get_java_vm().map_err(|error| error.to_string())?,
            object: env
                .new_global_ref(host)
                .map_err(|error| error.to_string())?,
        });
        let cipher = Arc::new(HostCipher(host.clone()));
        let sessions = Sessions::open(&data_dir, HostJar(host.clone()), cipher.clone())
            .map(Arc::new)
            .map_err(|error| format!("this Companion's pairings could not be read: {error}"))?;
        if sessions.quarantined() {
            let _ = host.unreadable();
        }
        let companion = runtime()
            .block_on(Companion::start(CompanionConfig {
                data_dir: data_dir.clone(),
                cipher,
            }))
            .map(Arc::new)
            .map_err(|error| format!("this Companion's key could not start it: {error}"))?;
        let route: Route = {
            let (companion, sessions) = (companion.clone(), sessions.clone());
            Arc::new(move |fleet, cookie| {
                let (companion, sessions) = (companion.clone(), sessions.clone());
                Box::pin(async move { companion.route(&sessions, &fleet, &cookie).await })
            })
        };
        let feed = Feed::routed(route.clone(), data_dir.join("alerts"));
        let alerts = Supervisor::new(feed, sessions.clone(), HostSink(host.clone()), RESCAN);
        Ok(Core {
            companion,
            alerts,
            sessions,
            host,
            route,
            live: Mutex::new(None),
            reporter: Mutex::new(None),
        })
    })();
    match result {
        Ok(core) => {
            state.core = Some(Arc::new(core));
            Ok(())
        }
        Err(message) => Err(message),
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_nativeStart<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    data_dir: JString<'local>,
    host: JObject<'local>,
    context: JObject<'local>,
) -> JString<'local> {
    unowned_env
        .with_env_no_catch(|env| match start(env, &data_dir, &host, &context) {
            Ok(()) => Ok(JString::null()),
            Err(message) => env.new_string(message),
        })
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_paired<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) -> jboolean {
    let sessions = STATE
        .lock()
        .unwrap()
        .core
        .as_ref()
        .map(|core| core.sessions.clone());
    sessions.is_some_and(|sessions| !sessions.listed().is_empty())
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_fleets<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
) -> JString<'local> {
    let sessions = STATE
        .lock()
        .unwrap()
        .core
        .as_ref()
        .map(|core| core.sessions.clone());
    let ids = sessions
        .map(|sessions| fleet_ids(&sessions))
        .unwrap_or_default()
        .join(" ");
    unowned_env
        .with_env_no_catch(|env| env.new_string(ids))
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_phone<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
) -> JString<'local> {
    let answer = catch_unwind(AssertUnwindSafe(|| {
        let core = core()?;
        Some(serde_json::json!({
            "id": core.companion.phone_id(),
            "core": VERSION,
            "fleets": core
                .sessions
                .listed()
                .iter()
                .map(fleet_json)
                .collect::<Vec<_>>(),
            "hosts": core
                .sessions
                .hosts()
                .iter()
                .map(|host| serde_json::json!({
                    "endpoint": host.endpoint,
                    "id": host.short,
                    "name": host.name,
                    "fleets": host.fleets.iter().map(fleet_json).collect::<Vec<_>>(),
                }))
                .collect::<Vec<_>>(),
        }))
    }))
    .ok()
    .flatten();
    unowned_env
        .with_env_no_catch(|env| match answer {
            Some(answer) => reply(env, answer),
            None => Ok(JString::null()),
        })
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_watch<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        runtime().block_on(async {
            core.alerts.start();
            *core.reporter.lock().unwrap() = Some(core.companion.report_every(REPORT, info));
        });
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_unwatch<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        core.reporter.lock().unwrap().take();
        core.alerts.stop();
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_sleep<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        core.quiet();
        runtime().block_on(async {
            core.companion.sleep().await;
            info(&core.companion.report().await);
        });
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_wake<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) -> jboolean {
    let Some(core) = core() else {
        return true;
    };
    match runtime().block_on(core.companion.wake()) {
        Ok(()) => true,
        Err(error) => {
            warn(&format!("wake failed: {error}"));
            false
        }
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_poll<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) -> jboolean {
    let Some(core) = core() else {
        return true;
    };
    runtime().block_on(async {
        let polled = core
            .companion
            .poll_asleep(core.alerts.poll_once(POLL_HOST))
            .await;
        info(&core.companion.report().await);
        match polled {
            Ok(Polled::Missed) => false,
            Ok(_) => true,
            Err(error) => {
                warn(&format!("poll failed: {error}"));
                false
            }
        }
    })
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_live<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
) {
    let fleet = unowned_env
        .with_env_no_catch(|env| -> jni::errors::Result<_> { Ok(text(env, &fleet).ok().flatten()) })
        .resolve::<LogErrorAndDefault>();
    let Some(fleet) = fleet else {
        return;
    };
    if let Some(core) = core() {
        core.start_live(&fleet);
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_unlive<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        core.stop_live();
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_resume<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        runtime().block_on(core.companion.reconnect_on_resume(|| core.alerts.nudge()));
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_networkChange<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        runtime().spawn(async move { core.companion.network_change(|| core.alerts.nudge()).await });
    }
}

fn text(env: &Env, value: &JString) -> Result<Option<String>, RequestError> {
    if value.as_raw().is_null() {
        return Ok(None);
    }
    value
        .try_to_string(env)
        .map(Some)
        .map_err(|_| RequestError::Malformed)
}

fn send(
    env: &Env,
    fleet: &JString,
    post: bool,
    path: &JString,
    csrf: &JString,
    body: &JByteArray,
) -> Result<Response, RequestError> {
    let core = STATE
        .lock()
        .unwrap()
        .core
        .clone()
        .ok_or(RequestError::NoHost)?;
    let fleet = text(env, fleet)?.ok_or(RequestError::Malformed)?;
    let path = text(env, path)?.ok_or(RequestError::Malformed)?;
    let csrf = text(env, csrf)?;
    let body = if body.as_raw().is_null() {
        Vec::new()
    } else {
        env.convert_byte_array(body)
            .map_err(|_| RequestError::Malformed)?
    };
    let cookie = core.sessions.cookie(&fleet).unwrap_or_default();
    let method = if post { Method::Post } else { Method::Get };
    let request = if path == "/api/v1/meta" {
        Request::host(method, &fleet, &path, &cookie)?
    } else {
        Request::new(method, &fleet, &path, Some(&cookie), csrf.as_deref(), &body)?
    };
    let response =
        runtime().block_on(
            core.companion
                .request(&core.sessions, &fleet, &cookie, &request),
        )?;
    if path == "/api/v1/meta"
        && response.status == 200
        && let Some((endpoint, _)) = core.sessions.route_of(&fleet, &cookie)
    {
        core.sessions.name_host(&endpoint, &response.body);
    }
    Ok(response)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_request<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
    post: jboolean,
    path: JString<'local>,
    csrf: JString<'local>,
    body: JByteArray<'local>,
) -> JString<'local> {
    unowned_env
        .with_env_no_catch(|env| {
            let sent = catch_unwind(AssertUnwindSafe(|| {
                send(env, &fleet, post, &path, &csrf, &body)
            }));
            reply(env, wire(sent))
        })
        .resolve::<LogErrorAndDefault>()
}

fn wire(sent: std::thread::Result<Result<Response, RequestError>>) -> serde_json::Value {
    match sent {
        Ok(Ok(response)) => serde_json::json!({
            "status": response.status,
            "body": String::from_utf8_lossy(&response.body),
        }),
        Ok(Err(error)) => serde_json::json!({ "failure": error.name() }),
        Err(_) => serde_json::json!({ "failure": RequestError::Malformed.name() }),
    }
}

fn upload(
    env: &Env,
    fleet: &JString,
    csrf: &JString,
    name: &JString,
    bytes: &JByteArray,
) -> Result<Response, RequestError> {
    let core = core().ok_or(RequestError::NoHost)?;
    let fleet = text(env, fleet)?.ok_or(RequestError::Malformed)?;
    let csrf = text(env, csrf)?.ok_or(RequestError::Malformed)?;
    let name = text(env, name)?.ok_or(RequestError::Malformed)?;
    if bytes.as_raw().is_null() {
        return Err(RequestError::Malformed);
    }
    let bytes = env
        .convert_byte_array(bytes)
        .map_err(|_| RequestError::Malformed)?;
    let cookie = core.sessions.cookie(&fleet).unwrap_or_default();
    let request = Request::upload(&fleet, &cookie, &csrf, &name, &bytes)?;
    runtime().block_on(
        core.companion
            .request(&core.sessions, &fleet, &cookie, &request),
    )
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_upload<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
    csrf: JString<'local>,
    name: JString<'local>,
    bytes: JByteArray<'local>,
) -> JString<'local> {
    unowned_env
        .with_env_no_catch(|env| {
            let sent = catch_unwind(AssertUnwindSafe(|| {
                upload(env, &fleet, &csrf, &name, &bytes)
            }));
            reply(env, wire(sent))
        })
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_file<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
    path: JString<'local>,
) -> JByteArray<'local> {
    unowned_env
        .with_env_no_catch(|env| {
            let sent = catch_unwind(AssertUnwindSafe(|| {
                send(
                    env,
                    &fleet,
                    false,
                    &path,
                    &JString::null(),
                    &JByteArray::null(),
                )
            }));
            match sent {
                Ok(Ok(response)) if response.status == 200 => {
                    env.byte_array_from_slice(&response.body)
                }
                _ => Ok(JByteArray::null()),
            }
        })
        .resolve::<LogErrorAndDefault>()
}

fn reply<'local>(
    env: &mut Env<'local>,
    answer: serde_json::Value,
) -> jni::errors::Result<JString<'local>> {
    env.new_string(answer.to_string())
        .or_else(|_| env.new_string(r#"{"failure":"malformed"}"#))
}

fn fleet_json(fleet: &hand_android_core::Listed) -> serde_json::Value {
    serde_json::json!({ "id": fleet.id, "name": fleet.name, "paired": fleet.paired })
}

fn fleet_ids(sessions: &Sessions<HostJar>) -> Vec<String> {
    sessions
        .listed()
        .into_iter()
        .map(|fleet| fleet.id)
        .collect()
}

fn core() -> Option<Arc<Core>> {
    STATE.lock().unwrap().core.clone()
}

fn failed(error: PairError) -> serde_json::Value {
    serde_json::json!({ "failure": error.name(), "message": error.message() })
}

fn offered(text: &str) -> serde_json::Value {
    let Some(core) = core() else {
        return failed(PairError::NoPending);
    };
    match core.companion.offer_link(&core.sessions, text) {
        Ok(offer) => {
            let mut answer = serde_json::json!({ "host": offer.host, "clash": offer.clash });
            if let Some(claim) = offer.claim {
                answer["claim"] = claim.into();
            }
            answer
        }
        Err(error) => serde_json::json!({ "failure": "link", "message": error.message() }),
    }
}

fn paired(name: &str) -> serde_json::Value {
    let Some(core) = core() else {
        return failed(PairError::NoPending);
    };
    match runtime().block_on(core.companion.pair(&core.sessions, name)) {
        Ok(fleets) => serde_json::json!({
            "fleets": fleets.iter().map(|fleet| serde_json::json!({ "id": fleet.id, "name": fleet.name })).collect::<Vec<_>>(),
        }),
        Err(error) => failed(error),
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_offer<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    link: JString<'local>,
) -> JString<'local> {
    unowned_env
        .with_env_no_catch(|env| {
            let answer = catch_unwind(AssertUnwindSafe(|| match text(env, &link) {
                Ok(Some(link)) => offered(&link),
                _ => serde_json::json!({ "failure": "link", "message": hand_android_core::LINK_REJECTED }),
            }))
            .unwrap_or_else(|_| failed(PairError::NoPending));
            reply(env, answer)
        })
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_pair<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    name: JString<'local>,
) -> JString<'local> {
    unowned_env
        .with_env_no_catch(|env| {
            let answer = catch_unwind(AssertUnwindSafe(|| match text(env, &name) {
                Ok(Some(name)) => paired(&name),
                _ => failed(PairError::Refused("bad_name")),
            }))
            .unwrap_or_else(|_| failed(PairError::NotSaved));
            reply(env, answer)
        })
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_cancel<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) {
    if let Some(core) = core() {
        core.companion.cancel_pairing();
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_unsaved<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) -> jboolean {
    core().is_some_and(|core| core.sessions.unsaved())
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_open<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    link: JString<'local>,
) -> JString<'local> {
    unowned_env
        .with_env_no_catch(|env| {
            let answer = catch_unwind(AssertUnwindSafe(|| {
                let paired = core()
                    .map(|core| fleet_ids(&core.sessions))
                    .unwrap_or_default();
                let opened = match text(env, &link) {
                    Ok(Some(link)) => open_paired(&paired, &link),
                    _ => Err(OPEN_REJECTED),
                };
                match opened {
                    Ok(open) => serde_json::json!({ "path": open.path() }),
                    Err(message) => serde_json::json!({ "message": message }),
                }
            }))
            .unwrap_or_else(|_| serde_json::json!({ "message": OPEN_REJECTED }));
            reply(env, answer)
        })
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_fallback<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    fleet: JString<'local>,
) -> JString<'local> {
    unowned_env
        .with_env_no_catch(|env| {
            let answer = catch_unwind(AssertUnwindSafe(|| {
                let board = text(env, &fleet).ok().flatten().and_then(|fleet| {
                    let core = core()?;
                    runtime().block_on(core.companion.fallback(&core.sessions, &fleet))
                });
                match board {
                    Some((port, cookie)) => serde_json::json!({ "port": port, "cookie": cookie }),
                    None => serde_json::json!({}),
                }
            }))
            .unwrap_or_else(|_| serde_json::json!({}));
            reply(env, answer)
        })
        .resolve::<LogErrorAndDefault>()
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_forget<'local>(
    _env: EnvUnowned<'local>,
    _class: JClass<'local>,
) -> jboolean {
    let forgotten = catch_unwind(AssertUnwindSafe(|| {
        core().is_none_or(|core| {
            runtime()
                .block_on(core.companion.forget_all(forget_alerts))
                .is_ok()
        })
    }));
    forgotten.unwrap_or(false)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_github_atqamz_handcompanion_Core_forgetHost<'local>(
    mut unowned_env: EnvUnowned<'local>,
    _class: JClass<'local>,
    host: JString<'local>,
) -> jboolean {
    let host = unowned_env
        .with_env_no_catch(|env| -> jni::errors::Result<_> { Ok(text(env, &host).ok().flatten()) })
        .resolve::<LogErrorAndDefault>();
    let Some(host) = host else {
        return false;
    };
    let forgotten = catch_unwind(AssertUnwindSafe(|| {
        core().is_none_or(|core| {
            runtime()
                .block_on(
                    core.companion
                        .forget_host(&host, || forget_host_alerts(&host)),
                )
                .is_ok()
        })
    }));
    forgotten.unwrap_or(false)
}
