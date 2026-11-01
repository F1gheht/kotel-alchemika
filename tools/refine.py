import json,sys,re
items=json.load(open(sys.argv[1]))['items']
fmt=json.load(open(sys.argv[2]))
names={x['UniqueName']:x.get('LocalizedNames') or {} for x in fmt}
def lst(x): return x if isinstance(x,list) else ([x] if x else [])
def mid(u,lvl):
    lvl=int(lvl or 0)
    return u if lvl==0 or '@' in u else f"{u}@{lvl}"
out={}
for it in lst(items.get('simpleitem')):
    u=it['@uniquename']
    if not re.match(r'^T\d_(LEATHER|METALBAR|CLOTH|PLANKS|STONEBLOCK)(_LEVEL\d)?$',u): continue
    reqs=[r for r in lst(it.get('craftingrequirements')) if not any('FACTION' in x['@uniquename'] for x in lst(r.get('craftresource')))]
    if not reqs: continue
    r=reqs[0]
    lvl=it.get('@enchantmentlevel','0')
    out[mid(u,lvl)]={'n':int(r.get('@amountcrafted',1)),'focus':int(float(r.get('@craftingfocus',0))),
        'ing':{mid(x['@uniquename'],x.get('@enchantmentlevel')):int(x['@count']) for x in lst(r.get('craftresource'))}}
# potion focus costs
pf={}
for it in lst(items.get('consumableitem')):
    u=it['@uniquename']
    if re.match(r'^T\d_POTION_',u):
        r=lst(it.get('craftingrequirements'))
        if r: pf[u]=int(float(r[0].get('@craftingfocus',0)))
ids=set(out)
for r in out.values(): ids|=set(r['ing'])
nm={i:{'ru':(names.get(i) or {}).get('RU-RU'),'en':(names.get(i) or {}).get('EN-US')} for i in ids}
miss=[i for i in ids if not nm[i]['ru']]
print(len(out),len(ids),'missing names',miss[:10],file=sys.stderr)
for k in ['T2_LEATHER','T3_LEATHER','T5_LEATHER_LEVEL2@2','T8_METALBAR_LEVEL4@4','T6_STONEBLOCK','T4_STONEBLOCK_LEVEL1@1','T4_PLANKS_LEVEL4@4']:
    print(k,out.get(k),nm.get(k),file=sys.stderr)
print('potion focus sample',pf.get('T8_POTION_LAVA'),pf.get('T5_POTION_MOB_RESET'),len(pf),file=sys.stderr)
json.dump({'recipes':out,'names':nm,'potionFocus':pf},open(sys.argv[3],'w'),ensure_ascii=False)
