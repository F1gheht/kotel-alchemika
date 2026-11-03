# Builds crafting data for equipment (weapons, armor, offhands, bags, capes, tools) from ao-bin-dumps.
import json,sys,re
items=json.load(open(sys.argv[1]))['items']
fmt=json.load(open(sys.argv[2]))
known=json.load(open(sys.argv[3]))['items']   # ids already named in data3.json
names={x['UniqueName']:x.get('LocalizedNames') or {} for x in fmt}
def lst(x): return x if isinstance(x,list) else ([x] if x else [])
def mid(u,lvl):
    lvl=int(lvl or 0)
    return u if lvl==0 or '@' in u else f"{u}@{lvl}"
def branch(x):
    sub=x.get('@shopsubcategory1') or ''; cat=x.get('@shopcategory'); u=x['@uniquename']
    if cat=='capes': return 'cape'
    if cat=='bags': return 'bag'
    if cat=='gathering': return 'tools' if '_TOOL_' in u else 'gatherer'
    if cat=='other' and sub=='guilds': return 'tools'
    if cat=='head' and sub=='other': return 'cloth_helmet'
    if cat=='offhands': return {'shieldtype':'shield','booktype':'book','torchtype':'torch'}[sub]
    return sub
def pick(reqs):
    reqs=lst(reqs)
    for r in reqs:
        if not any('TOKEN_FAVOR' in c['@uniquename'] for c in lst(r.get('craftresource'))): return r
    return reqs[0] if reqs else None
def rec(r):
    ing={}; nr=[]
    for c in lst(r.get('craftresource')):
        i=mid(c['@uniquename'],c.get('@enchantmentlevel')); ing[i]=ing.get(i,0)+int(c['@count'])
        if c.get('@maxreturnamount')=='0': nr.append(i)
    return int(float(r.get('@craftingfocus',0) or 0)), ing, nr
R={}; base={}; order=[]
for k in ['weapon','equipmentitem','transformationweapon']:
    for x in lst(items.get(k)):
        u=x['@uniquename']; m=re.match(r'^T([2-8])_(.+)$',u)
        if not m or not x.get('craftingrequirements'): continue
        if x.get('@shopcategory') not in ('weapons','armors','head','shoes','offhands','bags','capes','gathering','other'): continue
        if x.get('@shopcategory')=='other' and x.get('@shopsubcategory1')!='guilds': continue
        if not (names.get(u) or {}).get('EN-US'): continue  # unreleased items
        b=branch(x); t=int(m.group(1)); s=m.group(2)
        r0=pick(x['craftingrequirements']);
        if not r0: continue
        f,ing,nr=rec(r0); R[u]=[f,ing,nr]
        for e in lst((x.get('enchantments') or {}).get('enchantment')):
            lv=int(e['@enchantmentlevel']); re_=pick(e.get('craftingrequirements'))
            if re_ and lv<=4:
                f,ing,nr=rec(re_); R[f"{u}@{lv}"]=[f,ing,nr]
        if s not in base: base[s]={'b':b,'c':x.get('@craftingcategory') or '','t':[]}; order.append(s)
        base[s]['t'].append(t)
ids=set(R)
for v in R.values(): ids|=set(v[1])
need=sorted({re.sub(r'@\d$','',i) for i in ids if i not in known} - set(known))
nm={}
for i in need:
    n=names.get(i) or {}
    nm[i]={'ru':n.get('RU-RU') or n.get('EN-US') or i,'en':n.get('EN-US') or i}
for s in base: base[s]['t']=sorted(set(base[s]['t']))
out={'r':R,'base':base,'order':order,'names':nm}
json.dump(out,open(sys.argv[4],'w'),ensure_ascii=False,separators=(',',':'))
import collections
print(len(R),'recipes',len(base),'bases',len(nm),'names',collections.Counter(v['b'] for v in base.values()),file=sys.stderr)
print('missing names',[i for i in need if not names.get(i)][:20],file=sys.stderr)

# ---- compact form: one ingredient pattern per base item, expanded in the page ----
def pat(i,t,e):
    i=re.sub(rf'T{t}(?=_|$)','T*',i)
    if e:
        i=i.replace(f'_LEVEL{e}@{e}','~')
        i=re.sub(rf'@{e}$','^',i)
    return i
def expand(p,t,e):
    i=p.replace('T*',f'T{t}')
    if e: i=i.replace('~',f'_LEVEL{e}@{e}').replace('^',f'@{e}')
    else: i=i.replace('~','').replace('^','')
    return i
G={}
bad=0
for s,meta in base.items():
    pats={}; focus={}; ok=True; P=None
    for t in meta['t']:
        for e in range(5):
            u=f'T{t}_{s}'+(f'@{e}' if e else '')
            if u not in R: continue
            f,ing,nr=R[u]; focus[f'{t}.{e}']=f
            q=sorted([pat(i,t,e),c,1 if i in nr else 0] for i,c in ing.items())
            # e=0 patterns have no ~/^ markers: rebuild them from an enchanted variant when one exists
            pats[(t,e)]=q
    # choose the pattern of an enchanted variant (has markers), else plain
    cand=[q for (t,e),q in pats.items() if e]+[q for (t,e),q in pats.items() if not e]
    P=cand[0]
    for (t,e),q in pats.items():
        got={expand(p,t,e):(c,n) for p,c,n in P}
        u=f'T{t}_{s}'+(f'@{e}' if e else '')
        f,ing,nr=R[u]
        want={i:(c,1 if i in nr else 0) for i,c in ing.items()}
        if got!=want: ok=False; break
    if not ok:
        bad+=1; G[s]={**meta,'x':{f'T{t}_{s}'+(f'@{e}' if e else ''):R[f'T{t}_{s}'+(f'@{e}' if e else '')] for (t,e) in pats}}
    else:
        G[s]={**meta,'p':P,'f':focus}
out2={'base':G,'order':order,'names':nm}
json.dump(out2,open(sys.argv[4],'w'),ensure_ascii=False,separators=(',',':'))
print('compact; explicit bases:',bad,file=sys.stderr)
