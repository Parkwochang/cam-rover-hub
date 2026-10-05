const assert = require("node:assert/strict");
const test = require("node:test");
const vm = require("node:vm");
const fs = require("node:fs");
const path = require("node:path");

function cockpit() {
  const elements = new Map(), events = new Map(), sent = [];
  const node = (id) => {
    if (!elements.has(id)) elements.set(id, {
      value: id === "speed" ? "170" : "", dataset: {}, open: false, hidden: false,
      classList: { add() {}, remove() {}, toggle() {} }, handlers: {},
      addEventListener(name, fn) { this.handlers[name] = fn; },
      setAttribute() {}, removeAttribute() {}, setPointerCapture() {},
    });
    return elements.get(id);
  };
  const forward = node("forward"); forward.dataset.direction = "forward";
  const stop = node("stop"); stop.dataset.direction = "stop";
  class Socket {
    static OPEN = 1;
    constructor() { this.readyState = 1; }
    send(data) { sent.push(JSON.parse(data)); }
    close() {}
  }
  const context = vm.createContext({
    document: { hidden:false, activeElement:null, hasFocus:()=>true,
      getElementById:node, querySelectorAll:(q)=>q === "[data-direction]" ? [forward,stop] : [],
      addEventListener:(name,fn)=>events.set(name,fn) },
    location:{protocol:"https:",host:"example.invalid"}, localStorage:{getItem:()=>null},
    WebSocket:Socket, setTimeout:()=>1, clearTimeout(){}, setInterval(){},
    addEventListener:(name,fn)=>events.set(name,fn), AbortController, console,
  });
  const source = fs.readFileSync(path.join(__dirname,"../web/app.js"),"utf8");
  // Execute production handlers; omit only browser boot/network polling.
  vm.runInContext(source.slice(0,source.indexOf("new ResizeObserver(drawMap)")),context);
  const run = (code) => vm.runInContext(code,context);
  run('connectSocket(); socket.onopen(); videoHealthy=true; controlState={mode:"manual"}; updateControls();');
  const receive = (message) => {
    context.incoming = message;
    run('socket.onmessage({data:JSON.stringify(incoming)})');
  };
  return {node,events,sent,context,run,receive};
}

test("only matching activation ACK enables controls",async()=>{
  const c=cockpit();
  const done=c.node("manualMode").handlers.click();
  const request=c.sent.at(-1);
  assert.equal(request.type,"activate");
  assert.equal(request.speed,170,"manual activation must use the default speed");
  assert.equal(c.run("canDrive()"),false);
  c.receive({type:"ack",command:"activate",request_id:request.request_id+1,status:{mode:"manual"}});
  await Promise.resolve();
  assert.equal(c.run("activating"),true);
  c.receive({type:"ack",command:"activate",request_id:request.request_id,status:{mode:"manual",direction:"stop"}});
  await done;
  assert.equal(c.run("canDrive()"),true);
  assert.equal(c.sent.filter(x=>x.type==="stop").length,0,"activation must not send duplicate stops");
});

test("manual speed markup defaults to 170 without raising the minimum",()=>{
  const html=fs.readFileSync(path.join(__dirname,"../web/index.html"),"utf8");
  const input=html.match(/<input\b[^>]*\bid="speed"[^>]*>/)[0];
  assert.match(input,/\bvalue="170"/);
  assert.match(input,/\bmin="85"/);
  assert.match(html,/<span id="speedReadout">SPD 170<\/span>/);
  assert.match(html,/<output id="speedValue">170<\/output>/);
});

test("blur during activation cannot be undone by late ACK",async()=>{
  const c=cockpit();
  const done=c.node("manualMode").handlers.click();
  const request=c.sent.at(-1);
  c.events.get("blur")();
  c.receive({type:"ack",command:"activate",request_id:request.request_id,status:{mode:"manual"}});
  await done;
  assert.equal(c.run("canDrive()"),false);
  assert.ok(c.sent.some(x=>x.type==="stop"));
});

test("slow movement never accumulates a queue and stop bypasses it",()=>{
  const c=cockpit();
  c.run('send({type:"drive",direction:"forward"}); send({type:"drive",direction:"forward"}); stop();');
  assert.equal(c.sent.filter(x=>x.type==="drive").length,1);
  assert.equal(c.sent.at(-1).type,"stop");
  c.receive({type:"ack",command:"drive",request_id:c.sent[0].request_id});
  c.run('send({type:"drive",direction:"right"})');
  assert.equal(c.sent.filter(x=>x.type==="drive").length,2);
});

test("old status response cannot relatch successful activation",async()=>{
  const c=cockpit(); const resolvers=[];
  c.context.fetch=()=>new Promise(resolve=>resolvers.push(resolve));
  const poll=c.run("refreshStatus()");
  const done=c.node("manualMode").handlers.click();
  const request=c.sent.at(-1);
  c.receive({type:"ack",command:"activate",request_id:request.request_id,status:{mode:"manual"}});
  await done;
  resolvers[0]({ok:true,json:async()=>({mode:"manual",fault:"old fault"})});
  resolvers[1]({ok:true,json:async()=>({address:"cam-rover.local",video:{healthy:true}})});
  await poll;
  assert.equal(c.run("canDrive()"),true);
});

test("failed stop response does not recursively flood stop commands",()=>{
  const c=cockpit(); c.run("stop()");
  const request=c.sent.at(-1);
  c.receive({type:"error",command:"stop",request_id:request.request_id,message:"context deadline exceeded"});
  assert.equal(c.sent.filter(x=>x.type==="stop").length,1);
  assert.equal(c.run("canDrive()"),false);
});

test("mode selector defaults to manual and keeps automatic safety gates",()=>{
  const c=cockpit();
  assert.equal(c.node("manualMode").textContent,"수동 모드");
  assert.equal(c.node("modeState").textContent,"수동 모드");
  assert.equal(c.node("autoMode").textContent,"자동 모드");
  assert.equal(c.node("autoMode").disabled,true);
  c.run("autoEnabled=true; updateControls()");
  assert.equal(c.node("autoMode").disabled,false);
  c.run("videoHealthy=false; updateControls()");
  assert.equal(c.node("autoMode").disabled,true);
  c.run('videoHealthy=true; controlState={mode:"auto"}; updateControls()');
  assert.equal(c.node("modeState").textContent,"자동 모드");
  assert.equal(c.run("canDrive()"),false);
});
