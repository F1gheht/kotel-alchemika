from PIL import Image, ImageDraw, ImageFilter
import math
S=1024
im=Image.new('RGBA',(S,S),(0,0,0,0))
d=ImageDraw.Draw(im)
# rounded square background, deep green
d.rounded_rectangle([40,40,S-40,S-40],radius=200,fill=(29,52,42,255))
# subtle inner glow
g=Image.new('RGBA',(S,S),(0,0,0,0));gd=ImageDraw.Draw(g)
gd.ellipse([180,300,844,900],fill=(224,150,92,70));g=g.filter(ImageFilter.GaussianBlur(90));im=Image.alpha_composite(im,g);d=ImageDraw.Draw(im)
# cauldron body
d.chord([190,240,834,900],0,180,fill=(30,32,35,255))
# rim
d.rounded_rectangle([170,430,854,500],radius=35,fill=(58,62,66,255))
# potion liquid surface
d.ellipse([230,400,794,470],fill=(224,150,92,255))
d.ellipse([280,410,744,455],fill=(244,186,120,255))
# legs
for x in (300,700): d.rounded_rectangle([x,860,x+40,930],radius=12,fill=(22,24,26,255))
# bubbles
for (x,y,r) in [(430,330,38),(560,250,28),(500,170,20),(610,350,22)]:
    d.ellipse([x-r,y-r,x+r,y+r],outline=(244,186,120,255),width=14)
# highlight on body
d.arc([260,520,760,860],200,250,fill=(90,96,100,255),width=18)
im.save('/home/claude/desk/res/icon.png')
im.resize((256,256),Image.LANCZOS).save('/home/claude/desk/res/icon256.png')
im.save('/home/claude/desk/res/icon.ico',sizes=[(16,16),(24,24),(32,32),(48,48),(64,64),(128,128),(256,256)])
