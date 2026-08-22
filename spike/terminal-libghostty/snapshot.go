package main

/*
#cgo CFLAGS: -DGHOSTTY_STATIC -Ivendor/ghostty/include
#cgo LDFLAGS: vendor/ghostty/build/lib/libghostty-vt.a -framework CoreFoundation -framework ApplicationServices
#include <ghostty/vt.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdarg.h>
struct buf { uint8_t *p; size_t n; };
static bool collect(void *ctx, const uint8_t *data, size_t len) { struct buf *b=ctx; uint8_t *p=realloc(b->p,b->n+len); if(!p)return false; memcpy(p+b->n,data,len); b->p=p; b->n+=len; return true; }
static int append_bytes(struct buf *b, const uint8_t *data, size_t len) { uint8_t *p=realloc(b->p,b->n+len); if(!p)return 0; memcpy(p+b->n,data,len); b->p=p; b->n+=len; return 1; }
static int append_fmt(struct buf *b, const char *fmt, ...) { char s[64]; va_list ap; va_start(ap,fmt); int n=vsnprintf(s,sizeof(s),fmt,ap); va_end(ap); return n>0 && append_bytes(b,(uint8_t*)s,(size_t)n); }
static int append_style(struct buf *b, const GhosttyStyle *s) {
  if (!append_bytes(b,(const uint8_t*)"\033[0m",4)) return 0;
  if (s->bold && !append_bytes(b,(const uint8_t*)"\033[1m",4)) return 0;
  if (s->italic && !append_bytes(b,(const uint8_t*)"\033[3m",4)) return 0;
  if (s->faint && !append_bytes(b,(const uint8_t*)"\033[2m",4)) return 0;
  if (s->underline && !append_bytes(b,(const uint8_t*)"\033[4m",4)) return 0;
  if (s->fg_color.tag==GHOSTTY_STYLE_COLOR_PALETTE && s->fg_color.value.palette<16 && !append_fmt(b,"\033[%um",(unsigned)(s->fg_color.value.palette<8?30+s->fg_color.value.palette:90+s->fg_color.value.palette-8))) return 0;
  if (s->fg_color.tag==GHOSTTY_STYLE_COLOR_PALETTE && s->fg_color.value.palette>=16 && !append_fmt(b,"\033[38;5;%um",(unsigned)s->fg_color.value.palette)) return 0;
  if (s->fg_color.tag==GHOSTTY_STYLE_COLOR_RGB && !append_fmt(b,"\033[38;2;%u;%u;%um",s->fg_color.value.rgb.r,s->fg_color.value.rgb.g,s->fg_color.value.rgb.b)) return 0;
  if (s->bg_color.tag==GHOSTTY_STYLE_COLOR_PALETTE && s->bg_color.value.palette<16 && !append_fmt(b,"\033[%um",(unsigned)(s->bg_color.value.palette<8?40+s->bg_color.value.palette:100+s->bg_color.value.palette-8))) return 0;
  if (s->bg_color.tag==GHOSTTY_STYLE_COLOR_PALETTE && s->bg_color.value.palette>=16 && !append_fmt(b,"\033[48;5;%um",(unsigned)s->bg_color.value.palette)) return 0;
  if (s->bg_color.tag==GHOSTTY_STYLE_COLOR_RGB && !append_fmt(b,"\033[48;2;%u;%u;%um",s->bg_color.value.rgb.r,s->bg_color.value.rgb.g,s->bg_color.value.rgb.b)) return 0;
  return 1;
}
static int format_screen(GhosttyTerminal t, struct buf *b) {
  GhosttyFormatterTerminalOptions o=GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalOptions); o.emit=GHOSTTY_FORMATTER_FORMAT_VT; o.extra.size=sizeof(o.extra); o.extra.modes=true; o.extra.scrolling_region=true; o.extra.tabstops=true; o.extra.screen.size=sizeof(o.extra.screen); o.extra.screen.cursor=true; o.extra.screen.style=true; o.extra.screen.hyperlink=true; o.extra.screen.charsets=true;
  GhosttyFormatter f=NULL; if(ghostty_formatter_terminal_new(NULL,&f,t,o)!=GHOSTTY_SUCCESS)return 1; GhosttyWriter w={.write=collect,.userdata=b}; GhosttyResult r=ghostty_formatter_format(f,w); ghostty_formatter_free(f); return r==GHOSTTY_SUCCESS?0:2;
}
static int clone_terminal(GhosttyTerminal src, GhosttyTerminal *dst) {
  uint8_t *blob=NULL; size_t n=0; if(ghostty_snapshot_encode_alloc(src,NULL,&blob,&n)!=GHOSTTY_SUCCESS)return 1; GhosttySnapshotDecoder d=NULL; if(ghostty_snapshot_decoder_new_buf(NULL,&d,blob,n)!=GHOSTTY_SUCCESS){ghostty_free(NULL,blob,n);return 2;} GhosttyResult r=ghostty_snapshot_decoder_decode(d,dst); ghostty_snapshot_decoder_free(d); ghostty_free(NULL,blob,n); return r==GHOSTTY_SUCCESS?0:3;
}
int gh_snapshot(const uint8_t *input,size_t input_n,uint16_t cols,uint16_t rows,uint8_t **out,size_t *out_n) {
  GhosttyTerminal t=NULL,c=NULL; size_t max=64*1024; struct buf b={0}; if(ghostty_terminal_new(NULL,&t,cols,rows)!=GHOSTTY_SUCCESS)return 10; if(ghostty_terminal_set(t,GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES,&max)!=GHOSTTY_SUCCESS)return 11; ghostty_terminal_vt_write(t,input,input_n); if(clone_terminal(t,&c)!=0)return 12; GhosttyTerminalScreen active; if(ghostty_terminal_get(t,GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN,&active)!=GHOSTTY_SUCCESS)return 13; const uint8_t l[]="\033[?1049l", h[]="\033[?1049h"; ghostty_terminal_vt_write(c,l,sizeof(l)-1); if(!append_bytes(&b,l,sizeof(l)-1))return 14; if(format_screen(c,&b)!=0)return 15; ghostty_terminal_vt_write(c,h,sizeof(h)-1); if(!append_bytes(&b,h,sizeof(h)-1))return 16; if(format_screen(c,&b)!=0)return 17; if(active==GHOSTTY_TERMINAL_SCREEN_PRIMARY){ghostty_terminal_vt_write(c,l,sizeof(l)-1); if(!append_bytes(&b,l,sizeof(l)-1))return 18; if(format_screen(c,&b)!=0)return 19;} uint16_t x=0,y=0; if(ghostty_terminal_get(t,GHOSTTY_TERMINAL_DATA_CURSOR_X,&x)!=GHOSTTY_SUCCESS)return 20; if(ghostty_terminal_get(t,GHOSTTY_TERMINAL_DATA_CURSOR_Y,&y)!=GHOSTTY_SUCCESS)return 21; char cup[32]; int cup_n=snprintf(cup,sizeof(cup),"\033[%u;%uH",(unsigned)(y+1),(unsigned)(x+1)); if(!append_bytes(&b,(uint8_t*)cup,(size_t)cup_n))return 22; GhosttyStyle style=GHOSTTY_INIT_SIZED(GhosttyStyle); if(ghostty_terminal_get(t,GHOSTTY_TERMINAL_DATA_CURSOR_STYLE,&style)!=GHOSTTY_SUCCESS)return 23; if(!append_style(&b,&style))return 24; uint8_t *cont=NULL; size_t cn=0; if(ghostty_terminal_continuation_alloc(t,NULL,&cont,&cn)!=GHOSTTY_SUCCESS)return 25; if(!append_bytes(&b,cont,cn))return 26; ghostty_free(NULL,cont,cn); ghostty_terminal_free(c); ghostty_terminal_free(t); *out=b.p; *out_n=b.n; return 0;
}
static void gh_free(uint8_t *p){free(p);}
*/
import "C"

import (
	"flag"
	"fmt"
	"os"
	"unsafe"
)

func main() {
	inputPath := flag.String("input", "", "input VT byte stream")
	outPath := flag.String("out", "", "snapshot VT output")
	cut := flag.Int("cut", -1, "prefix length")
	cols := flag.Int("cols", 80, "columns")
	rows := flag.Int("rows", 24, "rows")
	flag.Parse()
	if *inputPath == "" || *outPath == "" || *cut < 0 {
		panic("-input, -out, and -cut are required")
	}
	in, err := os.ReadFile(*inputPath)
	if err != nil {
		panic(err)
	}
	if *cut > len(in) {
		panic("cut exceeds input")
	}
	var out *C.uint8_t
	var outN C.size_t
	if rc := C.gh_snapshot((*C.uint8_t)(unsafe.Pointer(&in[0])), C.size_t(*cut), C.uint16_t(*cols), C.uint16_t(*rows), &out, &outN); rc != 0 {
		panic(fmt.Sprintf("libghostty snapshot failed: %d", int(rc)))
	}
	data := C.GoBytes(unsafe.Pointer(out), C.int(outN))
	C.gh_free(out)
	if err := os.WriteFile(*outPath, data, 0600); err != nil {
		panic(err)
	}
}
