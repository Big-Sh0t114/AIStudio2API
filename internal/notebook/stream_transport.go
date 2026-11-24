package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Install the abort controller before awaiting fetch so cancellation can
// clean up even during the response headers.
const startNotebookStreamJS = `(async function(a) {
 const W=window.WIZ_global_data||{};
 if(location.origin!=='https://notebook.google.com'||!W.SNlM0e||!W.oPEP7c) return {signedOut:true};
 const old=globalThis.__aistudioNotebookStream;
 if(old) old.controller.abort();
 const state={key:a.key,controller:new AbortController(),reader:null,decoder:new TextDecoder()};
 globalThis.__aistudioNotebookStream=state;
 const q=new URLSearchParams({bl:W.cfb2h||'','f.sid':W.FdrFJe||'',hl:document.documentElement.lang||'en',_reqid:String(Math.floor(Math.random()*900000)+100000),rt:'c'});
 const body=JSON.stringify([null,JSON.stringify(a.params)]);
 const response=await fetch(a.path+'?'+q.toString(),{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded;charset=UTF-8','x-same-domain':'1'},body:new URLSearchParams({'f.req':body,at:W.SNlM0e}),credentials:'include',signal:state.controller.signal});
 if(new URL(response.url).origin!=='https://notebook.google.com') return {signedOut:true};
 if(response.ok&&response.body) state.reader=response.body.getReader();
 return {status:response.status};
})`

const pullNotebookStreamJS = `(async function(key) {
 const s=globalThis.__aistudioNotebookStream;
 if(!s||s.key!==key||!s.reader) throw new Error('Notebook stream reader is unavailable');
 const part=await s.reader.read();
 return {done:part.done,text:part.done?s.decoder.decode():s.decoder.decode(part.value,{stream:true})};
})`

const closeNotebookStreamJS = `(function(key) {
 const s=globalThis.__aistudioNotebookStream;
 if(s&&s.key===key){s.controller.abort();delete globalThis.__aistudioNotebookStream;}
 return true;
})`

func (b *browser) streamNotebook(ctx context.Context, params any, sourcePath string, onChunk func([]byte) error) error {
	if page, direct := b.shared.(HTTPPage); direct {
		result, err := b.executeHTTPWithStream(ctx, page, "", params, sourcePath, chatPath, onChunk)
		if err != nil {
			return err
		}
		return notebookResponseError(result)
	}

	key := newID()
	keyJSON, _ := json.Marshal(key)
	arg, err := json.Marshal(map[string]any{"key": key, "params": params, "path": chatPath})
	if err != nil {
		return err
	}
	// A bounded cleanup context is needed if the client disconnects.
	// Abort the browser fetch before returning the page lease.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_, _ = b.evaluate(cleanup, closeNotebookStreamJS+"("+string(keyJSON)+")")
	}()
	raw, err := b.evaluate(ctx, startNotebookStreamJS+"("+string(arg)+")")
	if err != nil {
		return err
	}
	var result browserResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if err := notebookResponseError(result); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := b.evaluate(ctx, pullNotebookStreamJS+"("+string(keyJSON)+")")
		if err != nil {
			return err
		}
		var part struct {
			Done bool   `json:"done"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &part); err != nil {
			return err
		}
		if part.Text != "" {
			if err := onChunk([]byte(part.Text)); err != nil {
				return err
			}
		}
		if part.Done {
			return nil
		}
	}
}

func (p *Provider) streamAnswer(ctx context.Context, a *account, params any, sourcePath string, titles map[string]string, emit func(string) error) (Answer, error) {
	if a.browser == nil {
		return Answer{}, errors.New("Notebook stream has no browser transport")
	}
	parser := notebookChatStream{titles: titles, emit: emit}
	if err := a.browser.streamNotebook(ctx, params, sourcePath, parser.feed); err != nil {
		return Answer{}, err
	}
	return parser.finish()
}
