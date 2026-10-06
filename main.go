// Synthetic LiveKit media load on a disposable runner. No server API secret.
// The only credentials accepted are short-lived tokens for two fixture rooms.
package main

import (
 "encoding/json"
 "fmt"
 "os"
 "os/signal"
 "runtime"
 "strconv"
 "strings"
 "sync"
 "sync/atomic"
 "syscall"
 "time"

 "github.com/go-logr/logr"
 "github.com/livekit/livekit-cli/v2/pkg/provider"
 lksdk "github.com/livekit/server-sdk-go/v2"
 "github.com/livekit/protocol/livekit"
 "github.com/livekit/protocol/logger"
 "github.com/pion/interceptor"
 rtpstats "github.com/pion/interceptor/pkg/stats"
 "github.com/pion/webrtc/v4"
)

type credential struct { Identity string `json:"identity"`; Token string `json:"token"` }
type configuration struct {
 URL string `json:"url"`
 Publishers []credential `json:"publishers"`
 Receivers []credential `json:"receivers"`
 Seconds int `json:"seconds"`
}
type trackState struct {
 SID string `json:"sid"`
 Kind string `json:"kind"`
 SSRC uint32 `json:"-"`
 Packets uint64 `json:"packets"`
 Lost int64 `json:"lost"`
 Bytes uint64 `json:"bytes"`
 Frames uint64 `json:"frames"`
 LastAt int64 `json:"lastAt"`
 Jitter float64 `json:"jitter"`
 Active bool `json:"active"`
}
type receiver struct {
 Identity string `json:"identity"`
 Room *lksdk.Room `json:"-"`
 Tracks []*trackState `json:"tracks"`
 Reconnects uint64 `json:"reconnects"`
 Disconnects uint64 `json:"disconnects"`
 Failures uint64 `json:"failures"`
 lock sync.Mutex
 getters []rtpstats.Getter
}
type receiverSnapshot struct {
 Identity string `json:"identity"`
 Tracks []*trackState `json:"tracks"`
 Reconnects uint64 `json:"reconnects"`
 Disconnects uint64 `json:"disconnects"`
 Failures uint64 `json:"failures"`
}
var stopping atomic.Bool
var requestedStop=make(chan struct{})
var stopOnce sync.Once

func newReceiver(c credential, url string) (*receiver, error) {
 r := &receiver{Identity:c.Identity, Tracks:[]*trackState{}}
 factory, err := rtpstats.NewInterceptor()
 if err != nil { return nil, err }
 factory.OnNewPeerConnection(func(_ string, getter rtpstats.Getter) {
  r.lock.Lock(); r.getters=append(r.getters,getter); r.lock.Unlock()
 })
 callback := &lksdk.RoomCallback{
  OnRoomMetadataChanged:func(metadata string){
   var control struct{LoadTestStop bool `json:"loadTestStop"`}
   if json.Unmarshal([]byte(metadata),&control)==nil&&control.LoadTestStop{stopOnce.Do(func(){close(requestedStop)})}
  },
  OnReconnecting:func(){r.lock.Lock();r.Reconnects++;r.lock.Unlock()},
  OnDisconnected:func(){if !stopping.Load(){r.lock.Lock();r.Disconnects++;r.lock.Unlock()}},
  ParticipantCallback:lksdk.ParticipantCallback{
   OnTrackSubscriptionFailed:func(_ string,_ *lksdk.RemoteParticipant){r.lock.Lock();r.Failures++;r.lock.Unlock()},
   OnTrackSubscribed:func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, _ *lksdk.RemoteParticipant){
    state:=&trackState{SID:pub.SID(),Kind:track.Kind().String(),SSRC:uint32(track.SSRC()),Active:true}
    r.lock.Lock();r.Tracks=append(r.Tracks,state);r.lock.Unlock()
    // Request the full screen and camera layer, regardless of subscription order.
    if track.Kind()==webrtc.RTPCodecTypeVideo {pub.SetVideoDimensions(1920,1080)}
    go func(){
     for {
      packet,_,err:=track.ReadRTP()
      if err!=nil{return}
      r.lock.Lock();state.LastAt=time.Now().UnixMilli();if packet.Marker {state.Frames++};r.lock.Unlock()
     }
    }()
   },
   OnTrackUnsubscribed:func(_ *webrtc.TrackRemote,pub *lksdk.RemoteTrackPublication,_ *lksdk.RemoteParticipant){
    r.lock.Lock();for _,state:=range r.Tracks{if state.SID==pub.SID(){state.Active=false}};r.lock.Unlock()
   },
  },
 }
 r.Room=lksdk.NewRoom(callback)
 err=r.Room.JoinWithToken(url,c.Token,lksdk.WithAutoSubscribe(true),lksdk.WithInterceptors([]interceptor.Factory{factory}))
 if err!=nil{return nil,fmt.Errorf("receiver join failed (%T)",err)}
 return r,nil
}
func (r *receiver) snapshot() receiverSnapshot {
 r.lock.Lock();defer r.lock.Unlock()
 out:=receiverSnapshot{Identity:r.Identity,Reconnects:r.Reconnects,Disconnects:r.Disconnects,Failures:r.Failures,Tracks:[]*trackState{}}
 for _,state:=range r.Tracks {
  copied:=*state
  for _,getter:=range r.getters {
   value:=getter.Get(state.SSRC)
   if value==nil||value.InboundRTPStreamStats.PacketsReceived==0 {continue}
   inbound:=value.InboundRTPStreamStats
   copied.Packets=inbound.PacketsReceived;copied.Lost=inbound.PacketsLost;copied.Bytes=inbound.BytesReceived;copied.Jitter=inbound.Jitter
   break
  }
  out.Tracks=append(out.Tracks,&copied)
 }
 return out
}
func publisher(c credential,url string)(*lksdk.Room,error){
 room:=lksdk.NewRoom(nil)
 if err:=room.JoinWithToken(url,c.Token,lksdk.WithAutoSubscribe(false));err!=nil{return nil,fmt.Errorf("publisher join failed (%T)",err)}
 audio,err:=provider.CreateAudioLooper()
 if err!=nil{room.Disconnect();return nil,err}
 audioTrack,err:=lksdk.NewLocalTrack(audio.Codec())
 if err==nil{err=audioTrack.StartWrite(audio,nil)}
 if err==nil{_,err=room.LocalParticipant.PublishTrack(audioTrack,&lksdk.TrackPublicationOptions{Name:"synthetic audio",Source:livekit.TrackSource_MICROPHONE})}
 if err!=nil{room.Disconnect();return nil,fmt.Errorf("audio publish failed (%T)",err)}
 videoTracks,err:=generatedVideoTracks()
 if err==nil{_,err=room.LocalParticipant.PublishSimulcastTrack(videoTracks,&lksdk.TrackPublicationOptions{Name:"synthetic camera",Source:livekit.TrackSource_CAMERA})}
 if err!=nil{room.Disconnect();return nil,fmt.Errorf("video publish failed (%T)",err)}
 return room,nil
}
func processStats() map[string]uint64 {
 var mem runtime.MemStats;runtime.ReadMemStats(&mem)
 out:=map[string]uint64{"heapBytes":mem.HeapAlloc,"goroutines":uint64(runtime.NumGoroutine())}
 if content,err:=os.ReadFile("/proc/self/stat");err==nil{
  fields:=strings.Fields(strings.SplitN(string(content),") ",2)[1])
  user,_:=strconv.ParseUint(fields[11],10,64);system,_:=strconv.ParseUint(fields[12],10,64);out["cpuTicks"]=user+system
 }
 if content,err:=os.ReadFile("/proc/self/status");err==nil{
  for _,line:=range strings.Split(string(content),"\n"){if strings.HasPrefix(line,"VmRSS:"){fields:=strings.Fields(line);value,_:=strconv.ParseUint(fields[1],10,64);out["rssBytes"]=value*1024}}
 }
 if content,err:=os.ReadFile("/proc/meminfo");err==nil{
  for _,line:=range strings.Split(string(content),"\n"){if strings.HasPrefix(line,"MemAvailable:"){fields:=strings.Fields(line);value,_:=strconv.ParseUint(fields[1],10,64);out["availableKiB"]=value}}
 }
 if content,err:=os.ReadFile("/proc/stat");err==nil{
  fields:=strings.Fields(strings.SplitN(string(content),"\n",2)[0])[1:]
  for i,field:=range fields{if i>=8{break};value,_:=strconv.ParseUint(field,10,64);out["cpuTotal"]+=value;if i==3||i==4{out["cpuIdle"]+=value};if i==7{out["cpuSteal"]=value}}
 }
 return out
}
func run() error {
 lksdk.SetLogger(logger.LogRLogger(logr.Discard()))
 var config configuration
 if err:=json.Unmarshal([]byte(os.Getenv("LIVE_CLASS_LOAD_FIXTURE")),&config);err!=nil{return fmt.Errorf("invalid fixture JSON")}
 if !strings.HasPrefix(config.URL,"wss://")||len(config.Publishers)<1||len(config.Publishers)>30||len(config.Receivers)!=len(config.Publishers)||config.Seconds<60||config.Seconds>10800{return fmt.Errorf("invalid bounded load configuration")}
 // Room-scoped tokens remain in process memory and are never printed.
 os.Unsetenv("LIVE_CLASS_LOAD_FIXTURE")
 publishers:=[]*lksdk.Room{};receivers:=[]*receiver{}
 defer func(){stopping.Store(true);for _,r:=range receivers{r.Room.Disconnect()};for _,r:=range publishers{r.Disconnect()}}()
 for i:=range config.Publishers{
  p,err:=publisher(config.Publishers[i],config.URL);if err!=nil{return err};publishers=append(publishers,p)
  r,err:=newReceiver(config.Receivers[i],config.URL);if err!=nil{return err};receivers=append(receivers,r)
  time.Sleep(500*time.Millisecond)
 }
 if len(receivers)==0{return fmt.Errorf("no report receiver")}
 deadline:=time.NewTimer(time.Duration(config.Seconds)*time.Second);defer deadline.Stop()
 ticker:=time.NewTicker(10*time.Second);defer ticker.Stop()
 signals:=make(chan os.Signal,1);signal.Notify(signals,syscall.SIGINT,syscall.SIGTERM);defer signal.Stop(signals)
 report:=func(final bool){
  stats:=make([]receiverSnapshot,0,len(receivers));for _,r:=range receivers{stats=append(stats,r.snapshot())}
  value:=map[string]any{"at":time.Now().UnixMilli(),"final":final,"publishers":len(publishers),"receivers":stats,"process":processStats()}
  encoded,err:=json.Marshal(value);if err!=nil{return}
  // Local monitoring reads fixture metadata through the existing SFU API.
  if len(encoded)<50000{receivers[0].Room.LocalParticipant.SetMetadata(string(encoded))}
  fmt.Printf("{\"at\":%d,\"publishers\":%d,\"receivers\":%d,\"final\":%t}\n",time.Now().UnixMilli(),len(publishers),len(receivers),final)
  if final{os.WriteFile("result.json",encoded,0600)}
 }
 report(false)
 for{select{case <-ticker.C:report(false);case <-deadline.C:report(true);return nil;case <-signals:report(true);return nil;case <-requestedStop:report(true);time.Sleep(300*time.Millisecond);return nil}}
}
func main(){if err:=run();err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}}
